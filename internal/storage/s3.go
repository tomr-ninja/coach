package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
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

const (
	maxParallelDownloads    = 8        // bound concurrent S3 downloads for checksums
	maxChecksumDownloadSize = 50 << 20 // 50 MiB — reject larger objects to avoid OOM
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
		client, s3err := NewS3Client(ctx, cfg)
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

			// Collect objects that need content download (no built-in checksum).
			type downloadTask struct {
				key  string
				size int64
			}
			var tasks []downloadTask

			for _, obj := range page.Contents {
				if strings.HasSuffix(*obj.Key, "/") {
					continue
				}

				// Prefer S3 Object Checksum SHA-256 (real content hash, no download).
				if tryChecksum {
					chk, csErr := getS3ChecksumSHA256(ctx, client, bucket, *obj.Key)
					if csErr == nil {
						checksums = append(checksums, chk)
						continue
					}
					if errors.Is(csErr, ErrNoChecksum) {
						tryChecksum = false
						fmt.Fprintf(os.Stderr, "checksum: S3 objects lack SHA256 metadata, downloading to compute content hashes — this may be slow\n")
					}
					// Transient errors fall through: object goes to download tasks,
					// but we keep trying checksum metadata for subsequent objects.
				}

				sz := int64(0)
				if obj.Size != nil {
					sz = *obj.Size
				}
				tasks = append(tasks, downloadTask{key: *obj.Key, size: sz})
			}

			// Download in parallel with bounded concurrency.
			if len(tasks) > 0 {
				var (
					wg    sync.WaitGroup
					sem   = make(chan struct{}, maxParallelDownloads)
					mu    sync.Mutex
					first error
				)
				for _, t := range tasks {
					wg.Add(1)
					go func(t downloadTask) {
						defer wg.Done()
						sem <- struct{}{}
						defer func() { <-sem }()

						chk, dlErr := downloadAndHash(ctx, client, bucket, t.key, t.size)
						mu.Lock()
						if dlErr != nil {
							if first == nil {
								first = maybeTransient(fmt.Errorf("checksum %s: %w", t.key, dlErr))
							}
						} else {
							checksums = append(checksums, chk)
						}
						// Warn about large objects so the user knows
						// when checksum computation may be slow.
						if t.size > 100<<20 {
							fmt.Fprintf(os.Stderr, "checksum: downloading large object %s (%.1f MiB) — this may take a while\n",
								t.key, float64(t.size)/(1<<20))
						}
						mu.Unlock()
					}(t)
				}
				wg.Wait()
				if first != nil {
					return nil, first
				}
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

	s3Client, err := NewS3Client(ctx, cfg)
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

// ErrObjectTooLarge means the S3 object exceeds the max checksum download size.
var ErrObjectTooLarge = errors.New("object too large for checksum download")

// ErrNoChecksum means the S3 object has no SHA256 checksum in its metadata.
var ErrNoChecksum = errors.New("no SHA256 checksum in object metadata")

// getS3ChecksumSHA256 retrieves the SHA-256 checksum from S3 object metadata.
// Returns ErrNoChecksum when the metadata is genuinely absent; other errors
// are transient and the caller should not disable checksum lookups.
func getS3ChecksumSHA256(ctx context.Context, client *s3.Client, bucket, key string) ([32]byte, error) {
	resp, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return [32]byte{}, err
	}
	if resp.ChecksumSHA256 == nil || *resp.ChecksumSHA256 == "" {
		return [32]byte{}, ErrNoChecksum
	}

	raw, err := base64.StdEncoding.DecodeString(*resp.ChecksumSHA256)
	if err != nil || len(raw) != 32 {
		return [32]byte{}, ErrNoChecksum
	}

	var out [32]byte
	copy(out[:], raw)

	return out, nil
}

// downloadAndHash downloads an S3 object and computes its SHA256 content hash,
// matching the sidecar's artifact.CollectChecksums.
func downloadAndHash(ctx context.Context, client *s3.Client, bucket, key string, listingSize int64) ([32]byte, error) {
	// Reject objects known to be too large from the listing, avoiding
	// an unnecessary GetObject round-trip.
	if listingSize > maxChecksumDownloadSize {
		return [32]byte{}, fmt.Errorf("%d bytes (max %d): %w", listingSize, maxChecksumDownloadSize, ErrObjectTooLarge)
	}

	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return [32]byte{}, fmt.Errorf("get object: %w", err)
	}
	defer resp.Body.Close()

	// Double-check with the authoritative ContentLength (listing size
	// may be stale if the object changed between list and get).
	if size := aws.ToInt64(resp.ContentLength); size > maxChecksumDownloadSize {
		return [32]byte{}, fmt.Errorf("%d bytes (max %d): %w", size, maxChecksumDownloadSize, ErrObjectTooLarge)
	}

	h := sha256.New()
	if _, err := io.Copy(h, resp.Body); err != nil {
		return [32]byte{}, fmt.Errorf("read body: %w", err)
	}

	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// NewS3Client creates an S3 client using the provided configuration.
func NewS3Client(ctx context.Context, cfg *config.Config) (*s3.Client, error) {
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
	if respErr, ok := errors.AsType[*smithyhttp.ResponseError](err); ok && respErr.HTTPStatusCode() >= 500 {
		return coacherrors.AsTransient(err)
	}

	return err
}
