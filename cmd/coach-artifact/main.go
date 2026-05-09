package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/tomr-ninja/coach/internal/artifact"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: coach-artifact <command> [args]\n")
		fmt.Fprintf(os.Stderr, "  fetch  --source s3:bucket/prefix --digest <hex> <data-dir>\n")
		fmt.Fprintf(os.Stderr, "  upload <local-dir> s3:bucket/prefix\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "fetch":
		cmdFetch(os.Args[2:])
	case "upload":
		cmdUpload(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func cmdFetch(args []string) {
	var source, digestHex, dataDir string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--source":
			i++
			if i < len(args) {
				source = args[i]
			}
		case "--digest":
			i++
			if i < len(args) {
				digestHex = args[i]
			}
		default:
			dataDir = args[i]
		}
	}

	if source == "" || digestHex == "" || dataDir == "" {
		fmt.Fprintf(os.Stderr, "usage: coach-artifact fetch --source s3:bucket/prefix --digest <hex> <data-dir>\n")
		os.Exit(1)
	}

	digest, err := hex.DecodeString(digestHex)
	if err != nil || len(digest) != 32 {
		fmt.Fprintf(os.Stderr, "invalid digest hex (need 64 chars): %v\n", err)
		os.Exit(1)
	}
	var digestArr [32]byte
	copy(digestArr[:], digest)

	bucket, prefix := parseURI(source)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	s3Client, err := newS3Client(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create s3 client: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create data dir: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Fetching data from s3://%s/%s...\n", bucket, prefix)

	// Fetch ignore file from S3 first.
	ignoreSet := fetchFilterFile(ctx, s3Client, bucket, prefix, ".coachignore")

	// List and download S3 objects.
	paginator := s3.NewListObjectsV2Paginator(s3Client, &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	})

	fileCount := 0
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "list s3 objects: %v\n", err)
			os.Exit(1)
		}
		for _, obj := range page.Contents {
			key := deref(obj.Key)
			if key == "" || strings.HasSuffix(key, "/") {
				continue
			}

			rel := strings.TrimPrefix(key, prefix)
			rel = strings.TrimPrefix(rel, "/")

			if rel == ".coachignore" {
				continue
			}

			// Apply ignore filter.
			if ignoreSet[rel] {
				fmt.Fprintf(os.Stderr, "  skip (ignored): %s\n", rel)
				continue
			}

			if err := downloadFile(ctx, s3Client, bucket, key, dataDir, rel); err != nil {
				fmt.Fprintf(os.Stderr, "download %s: %v\n", rel, err)
				os.Exit(1)
			}
			fileCount++
		}
	}

	if fileCount == 0 {
		fmt.Fprintf(os.Stderr, "no data files matched at s3://%s/%s\n", bucket, prefix)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Downloaded %d files. Computing fingerprint...\n", fileCount)

	checksums, err := artifact.CollectChecksums(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "collect checksums: %v\n", err)
		os.Exit(1)
	}

	fp := artifact.Fingerprint(digestArr, checksums)
	fmt.Printf("%x\n", fp)
}

func cmdUpload(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: coach-artifact upload <local-dir> s3:bucket/prefix\n")
		os.Exit(1)
	}

	localDir := args[0]
	s3Dest := args[1]
	bucket, prefix := parseURI(s3Dest)
	prefix = strings.TrimSuffix(prefix, "/") + "/"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	s3Client, err := newS3Client(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create s3 client: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Uploading to s3://%s/%s...\n", bucket, prefix)

	fileCount := 0
	err = filepath.WalkDir(localDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}

		key := prefix + rel

		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", rel, err)
		}
		defer f.Close()

		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &bucket,
			Key:    &key,
			Body:   f,
		})
		if err != nil {
			return fmt.Errorf("upload %s: %w", rel, err)
		}

		fmt.Fprintf(os.Stderr, "  uploaded %s\n", rel)
		fileCount++
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "upload: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Uploaded %d files.\n", fileCount)
}

// --- S3 helpers ---

func newS3Client(ctx context.Context) (*s3.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if ep := os.Getenv("AWS_ENDPOINT_URL"); ep != "" {
			o.BaseEndpoint = aws.String(ep)
			o.UsePathStyle = true
		}
	}), nil
}

func parseURI(uri string) (bucket, prefix string) {
	rest := strings.TrimPrefix(uri, "s3://")
	bucket, prefix, _ = strings.Cut(rest, "/")
	return bucket, prefix
}

func fetchFilterFile(ctx context.Context, client *s3.Client, bucket, prefix, name string) map[string]bool {
	key := prefix
	if !strings.HasSuffix(key, "/") {
		key += "/"
	}
	key += name

	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	set := make(map[string]bool)
	for _, line := range splitLines(string(data)) {
		line = trimSpace(line)
		if line != "" {
			set[line] = true
		}
	}
	return set
}

func downloadFile(ctx context.Context, client *s3.Client, bucket, key, dataDir, rel string) error {
	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	dstPath := filepath.Join(dataDir, rel)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}

	f, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
