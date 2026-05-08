package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/tomr-ninja/coach/internal/config"
	coacherrors "github.com/tomr-ninja/coach/internal/errors"
)

var (
	ErrNoObjects   = errors.New("no objects found at s3 location")
	ErrNotS3URI    = errors.New("not an s3 uri")
	ErrInvalidPath = errors.New("invalid s3 path")
)

// Checksums resolves SHA256 checksums for all objects under an S3 URI.
func Checksums(uri string, cfg *config.Config) ([][32]byte, error) {
	bucket, prefix, err := ParseURI(uri)
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

		tryChecksum := true

		for paginator.HasMorePages() {
			page, pageErr := paginator.NextPage(ctx)
			if pageErr != nil {
				return nil, maybeTransient(fmt.Errorf("list s3 objects: %w", pageErr))
			}
			for _, obj := range page.Contents {
				if strings.HasSuffix(*obj.Key, "/") {
					continue
				}

				// Prefer S3 Object Checksum SHA-256 (real content hash) over ETag.
				if tryChecksum {
					if chk, ok := fetchObjectChecksum(ctx, client, bucket, *obj.Key); ok {
						checksums = append(checksums, chk)
						continue
					}
					// First miss — stop trying; remaining objects likely same upload method.
					tryChecksum = false
				}

				// Fall back to ETag-based fingerprint.
				etag := strings.Trim(*obj.ETag, `"`)
				h := sha256.Sum256([]byte(etag))
				checksums = append(checksums, h)
			}
		}

		if len(checksums) == 0 {
			return nil, fmt.Errorf("%w: s3://%s/%s", ErrNoObjects, bucket, prefix)
		}
		return checksums, nil
	})
	return result, err
}

// ArtifactExists checks whether any objects exist under the given S3 path.
func ArtifactExists(ctx context.Context, cfg *config.Config, s3PathOut string) (bool, error) {
	bucket, prefix, err := ParsePathOut(s3PathOut)
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

// ParseURI splits an s3:// URI into bucket and prefix.
func ParseURI(uri string) (bucket, prefix string, err error) {
	rest := strings.TrimPrefix(uri, "s3://")
	if rest == "" || rest == uri {
		return "", "", fmt.Errorf("%w: %s", ErrNotS3URI, uri)
	}
	bucket, prefix, found := strings.Cut(rest, "/")
	if !found {
		return rest, "", nil
	}
	return bucket, prefix, nil
}

// ParsePathOut splits an S3 output path (without s3:// prefix) into bucket and prefix.
func ParsePathOut(path string) (bucket, prefix string, err error) {
	bucket, prefix, found := strings.Cut(path, "/")
	if !found {
		return "", "", fmt.Errorf("%w: %s", ErrInvalidPath, path)
	}
	return bucket, prefix, nil
}

// BuildEnvVars converts an S3Config into rclone environment variables.
func BuildEnvVars(s3 config.S3Config) map[string]string {
	provider := s3.Provider
	if provider == "" {
		provider = "AWS"
	}
	return map[string]string{
		"RCLONE_CONFIG_S3_TYPE":              "s3",
		"RCLONE_CONFIG_S3_PROVIDER":          provider,
		"RCLONE_CONFIG_S3_ACCESS_KEY_ID":     s3.AccessKeyID.Reveal(),
		"RCLONE_CONFIG_S3_SECRET_ACCESS_KEY": s3.SecretAccessKey.Reveal(),
		"RCLONE_CONFIG_S3_REGION":            s3.Region,
		"RCLONE_CONFIG_S3_ENDPOINT":          s3.Endpoint,
	}
}

// fetchObjectChecksum retrieves the SHA-256 checksum of an S3 object via HeadObject.
func fetchObjectChecksum(ctx context.Context, client *s3.Client, bucket, key string) ([32]byte, bool) {
	resp, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil || resp.ChecksumSHA256 == nil || *resp.ChecksumSHA256 == "" {
		return [32]byte{}, false
	}

	raw, err := base64.StdEncoding.DecodeString(*resp.ChecksumSHA256)
	if err != nil || len(raw) != 32 {
		return [32]byte{}, false
	}

	var out [32]byte
	copy(out[:], raw)

	return out, true
}

func newS3Client(ctx context.Context, cfg *config.Config) (*s3.Client, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if !cfg.S3.AccessKeyID.IsEmpty() {
		opts = append(opts,
			awsconfig.WithRegion(cfg.S3.Region),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
				cfg.S3.AccessKeyID.Reveal(),
				cfg.S3.SecretAccessKey.Reveal(),
				"",
			)),
		)
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
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
func maybeTransient(err error) error {
	if coacherrors.IsNetworkError(err) {
		return coacherrors.AsTransient(err)
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() >= 500 {
		return coacherrors.AsTransient(err)
	}
	return err
}
