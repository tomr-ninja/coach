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
)

var (
	errNoDataFiles = errors.New("no data files found")
	errNoS3Objects = errors.New("no objects found at s3 location")
	errNotS3URI    = errors.New("not an s3 uri")
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

	var opts []func(*config.LoadOptions) error
	if cfg != nil && cfg.S3.AccessKeyID != "" {
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

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg != nil && cfg.S3.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.S3.Endpoint)
			o.UsePathStyle = true
		}
	})

	var checksums [][32]byte
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list s3 objects: %w", err)
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
