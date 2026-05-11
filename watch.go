package coach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/tomr-ninja/coach/internal/config"
	"github.com/tomr-ninja/coach/internal/storage"
)

// WatchS3Log polls an S3 log file and prints new lines to stdout.
// Uses ETag to skip unchanged files and Range requests to fetch only new bytes.
// Exits automatically when a fresh DONE marker (written after watch started)
// appears, or when ctx is cancelled.
func WatchS3Log(ctx context.Context, cfg *config.Config, logS3URI string) {
	if logS3URI == "" {
		return
	}

	bucket, key := parseS3LogURI(logS3URI)
	if bucket == "" || key == "" {
		fmt.Fprintf(os.Stderr, "watch: invalid S3 log URI: %s\n", logS3URI)
		return
	}

	fmt.Fprintf(os.Stderr, "watch: tailing %s\n", logS3URI)

	s3Client, err := storage.NewS3Client(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: create S3 client: %v\n", err)
		return
	}

	doneKey := strings.Replace(key, "log.txt", "DONE", 1)

	w := &s3LogWatcher{
		client:  s3Client,
		bucket:  bucket,
		key:     key,
		doneKey: doneKey,
	}

	w.pollLoop(ctx)
}

const maxFetchBytes = 10 << 20 // 10 MiB cap per fetch to avoid OOM on large logs

type s3LogWatcher struct {
	client     *s3.Client
	bucket     string
	key        string
	doneKey    string
	lastETag   string
	shownBytes int64
	// sawDone404 tracks whether we've confirmed the DONE marker
	// didn't exist when we started watching (prevents stale-DONE races).
	sawDone404 bool
}

func (w *s3LogWatcher) pollLoop(ctx context.Context) {
	pollInterval := 5 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	w.fetchNew(ctx)

	for {
		if w.checkDone(ctx) {
			w.fetchNew(context.Background())
			return
		}

		select {
		case <-ctx.Done():
			w.fetchNew(context.Background())
			return
		case <-ticker.C:
			w.fetchNew(ctx)
		}
	}
}

// fetchNew checks if the S3 object has changed (via ETag), and if so,
// fetches only new bytes via Range request and prints them to stdout.
func (w *s3LogWatcher) fetchNew(ctx context.Context) {
	headCtx, headCancel := context.WithTimeout(ctx, 10*time.Second)
	defer headCancel()

	head, err := w.client.HeadObject(headCtx, &s3.HeadObjectInput{
		Bucket: aws.String(w.bucket),
		Key:    aws.String(w.key),
	})
	if err != nil {
		if isNotFound(err) {
			return
		}
		fmt.Fprintf(os.Stderr, "watch: head log: %v\n", err)
		return
	}

	etag := aws.ToString(head.ETag)
	size := aws.ToInt64(head.ContentLength)

	if etag == w.lastETag && size == w.shownBytes {
		return
	}

	// ETag changed → object was replaced (periodic or final upload).
	// Re-read from our previous offset (old bytes are unchanged for
	// append-only logs) but cap the read to avoid OOM.
	if etag != w.lastETag {
		newBytes := size - w.shownBytes
		if newBytes > maxFetchBytes || newBytes < 0 {
			// File replaced and grew beyond cap (or shrank —
			// log replaces the entire S3 object on each upload).
			// Reset offset to show only the tail portion.
			w.shownBytes = max(size-maxFetchBytes, 0)
		}
	}

	rangeVal := fmt.Sprintf("bytes=%d-", w.shownBytes)
	getCtx, getCancel := context.WithTimeout(ctx, 30*time.Second)
	defer getCancel()

	resp, err := w.client.GetObject(getCtx, &s3.GetObjectInput{
		Bucket: aws.String(w.bucket),
		Key:    aws.String(w.key),
		Range:  aws.String(rangeVal),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: fetch log: %v\n", err)
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: read log: %v\n", err)
		return
	}

	if len(data) > 0 {
		os.Stdout.Write(data)
		w.shownBytes += int64(len(data))
	}

	w.lastETag = etag
}

func (w *s3LogWatcher) checkDone(ctx context.Context) bool {
	headCtx, headCancel := context.WithTimeout(ctx, 10*time.Second)
	defer headCancel()

	_, err := w.client.HeadObject(headCtx, &s3.HeadObjectInput{
		Bucket: aws.String(w.bucket),
		Key:    aws.String(w.doneKey),
	})
	if err != nil {
		if isNotFound(err) {
			w.sawDone404 = true
		} else {
			fmt.Fprintf(os.Stderr, "watch: %v\n", err)
		}
		return false
	}

	// Only accept DONE markers that appeared after we confirmed
	// they didn't exist (sawDone404). Prevents races with stale
	// DONE markers from previous --force runs.
	if !w.sawDone404 {
		fmt.Fprintf(os.Stderr, "watch: ignoring stale DONE marker\n")
		return false
	}

	fmt.Fprintf(os.Stderr, "watch: job finished\n")
	return true
}

func parseS3LogURI(uri string) (bucket, key string) {
	rest := strings.TrimPrefix(uri, "s3://")
	bucket, key, _ = strings.Cut(rest, "/")
	return bucket, key
}

func isNotFound(err error) bool {
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		return respErr.HTTPStatusCode() == http.StatusNotFound
	}
	s := err.Error()
	return strings.Contains(s, "404") ||
		strings.Contains(s, "NotFound") ||
		strings.Contains(s, "NoSuchKey")
}
