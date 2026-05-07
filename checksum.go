package coach

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	coacherrors "github.com/tomr-ninja/coach/internal/errors"
)

var (
	errNoDataFiles   = errors.New("no data files found")
	errNoS3Objects   = errors.New("no objects found at s3 location")
	errNotS3URI      = errors.New("not an s3 uri")
	errInvalidS3Path = errors.New("invalid s3 path")
)

func ResolveChecksums(source string, cfg *Config) ([][32]byte, error) {
	if strings.HasPrefix(source, "s3://") {
		checksums, err := s3Checksums(source, cfg)
		if err != nil {
			return nil, fmt.Errorf("s3 checksums for %s: %w", source, err)
		}
		if len(checksums) == 0 {
			return nil, errNoS3Objects
		}
		return checksums, nil
	}

	checksums, err := CollectDataChecksums(source)
	if err != nil {
		return nil, fmt.Errorf("local checksums for %s: %w", source, err)
	}
	if len(checksums) == 0 {
		return nil, errNoDataFiles
	}
	return checksums, nil
}

func s3Checksums(uri string, cfg *Config) ([][32]byte, error) {
	bucket, prefix, err := parseS3URI(uri)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := coacherrors.Retry(ctx, coacherrors.RetryConfig{}, func() ([][32]byte, error) {
		client, s3err := newS3Client(ctx, cfg)
		if s3err != nil {
			return nil, maybeTransient(s3err)
		}

		var checksums [][32]byte
		paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
			Bucket: &bucket,
			Prefix: &prefix,
		})

		for paginator.HasMorePages() {
			page, pageErr := paginator.NextPage(ctx)
			if pageErr != nil {
				return nil, maybeTransient(fmt.Errorf("list s3 objects: %w", pageErr))
			}
			for _, obj := range page.Contents {
				if strings.HasSuffix(*obj.Key, "/") {
					continue
				}
				etag := strings.Trim(*obj.ETag, `"`)
				h := sha256.Sum256([]byte(etag))
				checksums = append(checksums, h)
			}
		}

		if len(checksums) == 0 {
			return nil, fmt.Errorf("%w: s3://%s/%s", errNoS3Objects, bucket, prefix)
		}
		return checksums, nil
	})
	return result, err
}

func parseS3URI(uri string) (bucket, prefix string, err error) {
	rest := strings.TrimPrefix(uri, "s3://")
	if rest == uri {
		return "", "", fmt.Errorf("%w: %s", errNotS3URI, uri)
	}
	bucket, prefix, found := strings.Cut(rest, "/")
	if !found {
		return rest, "", nil
	}
	return bucket, prefix, nil
}

func s3ArtifactExists(ctx context.Context, cfg *Config, s3PathOut string) (bool, error) {
	bucket, prefix, err := parseS3PathOut(s3PathOut)
	if err != nil {
		return false, err
	}

	s3Client, err := newS3Client(ctx, cfg)
	if err != nil {
		return false, fmt.Errorf("create s3 client: %w", err)
	}

	resp, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  &bucket,
		Prefix:  &prefix,
		MaxKeys: aws.Int32(1),
	})
	if err != nil {
		return false, fmt.Errorf("list s3 objects: %w", err)
	}

	return len(resp.Contents) > 0, nil
}

func parseS3PathOut(path string) (bucket, prefix string, err error) {
	bucket, prefix, found := strings.Cut(path, "/")
	if !found {
		return "", "", fmt.Errorf("%w: %s", errInvalidS3Path, path)
	}
	return bucket, prefix, nil
}

func newS3Client(ctx context.Context, cfg *Config) (*s3.Client, error) {
	var opts []func(*config.LoadOptions) error
	if cfg.S3.AccessKeyID != "" {
		opts = append(opts,
			config.WithRegion(cfg.S3.Region),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
				cfg.S3.AccessKeyID,
				cfg.S3.SecretAccessKey,
				"",
			)),
		)
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.S3.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.S3.Endpoint)
			o.UsePathStyle = true
		}
	}), nil
}

// maybeTransient wraps an S3 error as transient if it is a network error or HTTP 5xx.
// Authentication errors (4xx) are left as permanent.
func maybeTransient(err error) error {
	if coacherrors.IsNetworkError(err) {
		return coacherrors.AsTransient(err)
	}
	if respErr, ok := errors.AsType[*smithyhttp.ResponseError](err); ok && respErr.HTTPStatusCode() >= 500 {
		return coacherrors.AsTransient(err)
	}
	return err
}
