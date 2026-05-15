package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/tomr-ninja/coach/internal/artifact"
	"github.com/tomr-ninja/coach/internal/storage"
)

var (
	errInvalidDigestHex  = errors.New("invalid digest hex")
	errS3Client          = errors.New("create s3 client")
	errCreateDataDir     = errors.New("create data dir")
	errListS3Objects     = errors.New("list s3 objects")
	errDownload          = errors.New("download")
	errNoMatchingS3Files = errors.New("no data files matched")
	errCollectChecksums  = errors.New("collect checksums")
	errUploadFailed      = errors.New("upload")
	errFinishMarshal     = errors.New("finish hook: marshal payload")
	errFinishRequest     = errors.New("finish hook: create request")
	errFinishPost        = errors.New("finish hook: POST failed")
	errFinishHTTP        = errors.New("finish hook: got HTTP error")
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: coach-sidecar <command> [args]\n")
		fmt.Fprintf(os.Stderr, "  fetch     --source s3:bucket/prefix --digest <hex> <data-dir>\n")
		fmt.Fprintf(os.Stderr, "  upload    <local-dir> s3:bucket/prefix\n")
		fmt.Fprintf(os.Stderr, "  finish    --output-dir <dir> [--fingerprint <hex>] --exit-code <int> [--stderr-file <path>] [--upload-ok <bool>]\n")
		fmt.Fprintf(os.Stderr, "  log-tee      --output-dir <dir> [--flush-interval <seconds>]\n")
		fmt.Fprintf(os.Stderr, "  upload-file  <local-file> s3:bucket/key\n")
		fmt.Fprintf(os.Stderr, "  delete-file  s3:bucket/key\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "fetch":
		cmdFetch(os.Args[2:])
	case "upload":
		cmdUpload(os.Args[2:])
	case "finish":
		cmdFinish(os.Args[2:])
	case "log-tee":
		cmdLogTee(os.Args[2:])
	case "upload-file":
		cmdUploadFile(os.Args[2:])
	case "delete-file":
		cmdDeleteFile(os.Args[2:])
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
		fmt.Fprintf(os.Stderr, "usage: coach-sidecar fetch --source s3:bucket/prefix --digest <hex> <data-dir>\n")
		os.Exit(1)
	}

	fp, err := doFetch(source, digestHex, dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%x\n", fp)
}

func doFetch(source, digestHex, dataDir string) ([32]byte, error) {
	digest, err := hex.DecodeString(digestHex)
	if err != nil || len(digest) != 32 {
		return [32]byte{}, fmt.Errorf("%w: %w", errInvalidDigestHex, err)
	}
	var digestArr [32]byte
	copy(digestArr[:], digest)

	bucket, prefix := parseURI(source)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	s3Client, err := newS3Client(ctx)
	if err != nil {
		return [32]byte{}, fmt.Errorf("%w: %w", errS3Client, err)
	}

	//nolint:gosec // CLI sidecar: dataDir comes from container entrypoint args
	if mErr := os.MkdirAll(dataDir, 0o755); mErr != nil {
		return [32]byte{}, fmt.Errorf("%w: %w", errCreateDataDir, mErr)
	}

	fmt.Fprintf(os.Stderr, "Fetching data from s3://%s/%s...\n", bucket, prefix)

	ignoreSet := fetchFilterFile(ctx, s3Client, bucket, prefix, ".coachignore")

	paginator := s3.NewListObjectsV2Paginator(s3Client, &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	})

	fileCount := 0
	var page *s3.ListObjectsV2Output
	for paginator.HasMorePages() {
		page, err = paginator.NextPage(ctx)
		if err != nil {
			return [32]byte{}, fmt.Errorf("%w: %w", errListS3Objects, err)
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

			if ignoreSet[rel] {
				fmt.Fprintf(os.Stderr, "  skip (ignored): %s\n", rel)
				continue
			}

			if dErr := downloadFile(ctx, s3Client, bucket, key, dataDir, rel); dErr != nil {
				return [32]byte{}, fmt.Errorf("%w: %s: %w", errDownload, rel, dErr)
			}
			fileCount++
		}
	}

	if fileCount == 0 {
		return [32]byte{}, fmt.Errorf("%w: s3://%s/%s", errNoMatchingS3Files, bucket, prefix)
	}

	fmt.Fprintf(os.Stderr, "Downloaded %d files. Computing fingerprint...\n", fileCount)

	checksums, err := storage.ChecksumsWithClient(ctx, s3Client, bucket, prefix)
	if err != nil {
		return [32]byte{}, fmt.Errorf("%w: %w", errCollectChecksums, err)
	}

	fp := artifact.Fingerprint(digestArr, checksums)
	return fp, nil
}

func cmdUpload(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: coach-sidecar upload <local-dir> s3:bucket/prefix\n")
		os.Exit(1)
	}

	localDir := args[0]
	s3Dest := args[1]

	if err := doUpload(localDir, s3Dest); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func cmdUploadFile(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: coach-sidecar upload-file <local-file> s3:bucket/key\n")
		os.Exit(1)
	}

	localFile := args[0]
	s3Dest := args[1]

	if err := doUploadFile(localFile, s3Dest); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

const maxUploadFileSize = 50 << 20 // 50 MiB cap to avoid OOM

func doUploadFile(localFile, s3Dest string) error {
	bucket, key := parseURI(s3Dest)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s3Client, err := newS3Client(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", errS3Client, err)
	}

	// Read file capped at maxUploadFileSize to prevent OOM on
	// unexpectedly large log files.
	// #nosec G703 -- localFile is from the sidecar's trusted localDir
	info, err := os.Stat(localFile)
	if err != nil {
		return fmt.Errorf("stat %s: %w", localFile, err)
	}
	readSize := info.Size()
	if readSize > maxUploadFileSize {
		fmt.Fprintf(os.Stderr, "upload-file: %s is %d bytes, capping at %d bytes\n",
			localFile, readSize, maxUploadFileSize)
		readSize = maxUploadFileSize
	}

	// #nosec G703 -- localFile is from the sidecar's trusted localDir
	f, err := os.Open(localFile)
	if err != nil {
		return fmt.Errorf("open %s: %w", localFile, err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, readSize))
	if err != nil {
		return fmt.Errorf("read %s: %w", localFile, err)
	}

	_, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   bytes.NewReader(data),
	})
	if putErr != nil {
		return fmt.Errorf("%w: %w", errUploadFailed, putErr)
	}

	return nil
}

func cmdDeleteFile(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: coach-sidecar delete-file s3:bucket/key\n")
		os.Exit(1)
	}

	if err := doDeleteFile(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func doDeleteFile(s3URI string) error {
	bucket, key := parseURI(s3URI)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s3Client, err := newS3Client(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", errS3Client, err)
	}

	_, err = s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete s3://%s/%s: %w", bucket, key, err)
	}

	return nil
}

func doUpload(localDir, s3Dest string) error {
	bucket, prefix := parseURI(s3Dest)
	prefix = strings.TrimSuffix(prefix, "/") + "/"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	s3Client, err := newS3Client(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", errS3Client, err)
	}

	fmt.Fprintf(os.Stderr, "Uploading to s3://%s/%s...\n", bucket, prefix)

	// Collect all files, skipping .coach/log.txt and .coach/DONE.
	// They are uploaded separately after the pipeline completes.
	type fileEntry struct {
		path string
		rel  string
	}
	var files []fileEntry

	//nolint:gosec // CLI sidecar: localDir comes from container entrypoint args
	err = filepath.WalkDir(localDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(localDir, path)
		if relErr != nil {
			return relErr
		}
		// Skip log and DONE marker — they're uploaded after the pipeline finishes.
		if rel == filepath.Join(".coach", "log.txt") || rel == filepath.Join(".coach", "DONE") {
			return nil
		}
		files = append(files, fileEntry{path: path, rel: rel})
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w: %w", errUploadFailed, err)
	}

	fileCount := 0
	for _, fe := range files {
		key := prefix + fe.rel

		// #nosec G703 -- fe.path is from WalkDir in trusted localDir
		f, openErr := os.Open(fe.path)
		if openErr != nil {
			return fmt.Errorf("open %s: %w", fe.rel, openErr)
		}

		_, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &bucket,
			Key:    &key,
			Body:   f,
		})
		f.Close()
		if putErr != nil {
			return fmt.Errorf("upload %s: %w", fe.rel, putErr)
		}

		fmt.Fprintf(os.Stderr, "  uploaded %s\n", fe.rel)
		fileCount++
	}

	fmt.Fprintf(os.Stderr, "Uploaded %d files.\n", fileCount)
	return nil
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
	//nolint:gosec // CLI sidecar: dstPath derived from trusted dataDir + rel
	if mErr := os.MkdirAll(filepath.Dir(dstPath), 0o755); mErr != nil {
		return mErr
	}

	//nolint:gosec // CLI sidecar: dstPath derived from trusted dataDir + rel
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
	for i := range len(s) {
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
	for s != "" && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for s != "" && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// finishPayload is the JSON body sent to the webhook on run completion.
type finishPayload struct {
	Fingerprint string          `json:"fingerprint"`
	Success     bool            `json:"success"`
	Error       string          `json:"error,omitempty"`
	UploadOk    bool            `json:"uploadOk"`
	Metrics     json.RawMessage `json:"metrics,omitempty"`
	Meta        json.RawMessage `json:"meta,omitempty"`
	Run         json.RawMessage `json:"run,omitempty"`
}

func cmdFinish(args []string) {
	var outputDir, fingerprint, stderrFile string
	exitCode := -1
	uploadOk := true

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output-dir":
			i++
			if i < len(args) {
				outputDir = args[i]
			}
		case "--fingerprint":
			i++
			if i < len(args) {
				fingerprint = args[i]
			}
		case "--exit-code":
			i++
			if i < len(args) {
				if v, err := strconv.Atoi(args[i]); err == nil {
					exitCode = v
				}
			}
		case "--stderr-file":
			i++
			if i < len(args) {
				stderrFile = args[i]
			}
		case "--upload-ok":
			i++
			if i < len(args) {
				if ok, err := strconv.ParseBool(args[i]); err == nil {
					uploadOk = ok
				}
			}
		}
	}

	if outputDir == "" || exitCode < 0 {
		fmt.Fprintf(os.Stderr, "usage: coach-sidecar finish --output-dir <dir> [--fingerprint <hex>] --exit-code <int> [--stderr-file <path>] [--upload-ok <bool>]\n")
		os.Exit(1)
	}

	if err := doFinish(outputDir, fingerprint, stderrFile, exitCode, uploadOk); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func doFinish(outputDir, fingerprint, stderrFile string, exitCode int, uploadOk bool) error {
	webhookURL := os.Getenv("COACH_WEBHOOK_URL")
	if webhookURL == "" {
		fmt.Fprintf(os.Stderr, "COACH_WEBHOOK_URL not set, skipping finish hook\n")
		return nil
	}

	payload := finishPayload{
		Fingerprint: fingerprint,
		Success:     exitCode == 0,
		UploadOk:    uploadOk,
	}

	if exitCode != 0 && stderrFile != "" {
		//nolint:gosec // CLI sidecar: stderrFile comes from container entrypoint args
		if raw, err := os.ReadFile(stderrFile); err == nil {
			msg := strings.TrimSpace(string(raw))
			if runes := []rune(msg); len(runes) > 256 {
				msg = string(runes[:256])
			}
			payload.Error = msg
			fmt.Fprintf(os.Stderr, "finish hook: captured stderr (%d chars)\n", len(msg))
		}
	}

	const maxFileSize = 1 << 20 // 1 MiB
	//nolint:gosec // CLI sidecar: outputDir comes from container entrypoint args
	if metricsData, err := os.ReadFile(filepath.Join(outputDir, "metrics.json")); err == nil && len(metricsData) <= maxFileSize && json.Valid(metricsData) {
		payload.Metrics = metricsData
		fmt.Fprintf(os.Stderr, "finish hook: loaded metrics.json\n")
	}

	//nolint:gosec // CLI sidecar: outputDir comes from container entrypoint args
	if metaData, err := os.ReadFile(filepath.Join(outputDir, "meta.json")); err == nil && len(metaData) <= maxFileSize && json.Valid(metaData) {
		payload.Meta = metaData
		fmt.Fprintf(os.Stderr, "finish hook: loaded meta.json\n")
	}

	//nolint:gosec // CLI sidecar: outputDir comes from container entrypoint args
	if runData, err := os.ReadFile(filepath.Join(outputDir, ".coach", "run.json")); err == nil && len(runData) <= maxFileSize && json.Valid(runData) {
		payload.Run = runData
		fmt.Fprintf(os.Stderr, "finish hook: loaded run.json\n")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: %w", errFinishMarshal, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	//nolint:gosec // SSRF false positive: webhookURL set by trusted wrapper via COACH_WEBHOOK_URL env var
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %w", errFinishRequest, err)
	}
	req.Header.Set("Content-Type", "application/json")

	//nolint:gosec // SSRF false positive: same trusted COACH_WEBHOOK_URL env var
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", errFinishPost, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: %d", errFinishHTTP, resp.StatusCode)
	}

	fmt.Fprintf(os.Stderr, "finish hook: posted successfully (%d)\n", resp.StatusCode)
	return nil
}
