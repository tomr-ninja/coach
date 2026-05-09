package validate

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/robfig/cron/v3"

	"github.com/tomr-ninja/coach/protocol"
)

var (
	ErrModelImageEmpty     = errors.New("model image is empty")
	ErrModelImageNoTag     = errors.New("model image has no explicit tag or digest")
	ErrDirEmpty            = errors.New("directory path is empty")
	ErrDirMissing          = errors.New("directory does not exist")
	ErrDirNotDir           = errors.New("path is not a directory")
	ErrDirS3Format         = errors.New("invalid s3 directory path format")
	ErrCronExpression      = errors.New("invalid cron expression")
	ErrScriptNameEmpty     = errors.New("script name is empty")
	ErrScriptNamePathSep   = errors.New("script name must not contain path separators")
	ErrScriptNameTraversal = errors.New("script name must not contain '..'")
	ErrMixedLocalS3        = errors.New("data source and output must both be local or both be s3")
	ErrEmptySubmitID       = errors.New("submit result has empty ID")
	ErrEmptyStatusID       = errors.New("status result has empty ID")
	ErrEmptyState          = errors.New("status result has empty State")
)

// ModelImage rejects empty strings and images without an explicit tag
// or digest, because Docker's default resolution can be surprising.
func ModelImage(image string) error {
	if strings.TrimSpace(image) == "" {
		return ErrModelImageEmpty
	}
	last := image
	if i := strings.LastIndex(image, "/"); i >= 0 {
		last = image[i+1:]
	}
	if !strings.Contains(last, ":") && !strings.Contains(last, "@") {
		return ErrModelImageNoTag
	}
	return nil
}

// DirPath validates that a local directory path exists, or that an S3 URI
// is well-formed. Callers should wrap errors to indicate which path is
// being validated (e.g. "validate data path: %w", "validate output dir: %w").
func DirPath(p string) error {
	if strings.TrimSpace(p) == "" {
		return ErrDirEmpty
	}
	if strings.HasPrefix(p, "s3://") {
		if len(p) <= len("s3://") {
			return ErrDirS3Format
		}
		return nil
	}
	info, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrDirMissing, p)
		}
		return fmt.Errorf("stat %s: %w", p, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s", ErrDirNotDir, p)
	}
	return nil
}

// Cron parses a cron expression using the standard 5-field format.
// An empty expression is treated as valid (one-off job).
func Cron(expr string) error {
	if strings.TrimSpace(expr) == "" {
		return nil
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if _, err := parser.Parse(expr); err != nil {
		return fmt.Errorf("%w: %w", ErrCronExpression, err)
	}
	return nil
}

// ScriptName rejects path traversal and empty identifiers.
func ScriptName(name string) error {
	if strings.TrimSpace(name) == "" {
		return ErrScriptNameEmpty
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return ErrScriptNamePathSep
	}
	if strings.Contains(name, "..") {
		return ErrScriptNameTraversal
	}
	return nil
}

// LocalVsS3 ensures data source and output are either both local or both S3.
func LocalVsS3(dataSource, outputURI string) error {
	dataIsS3 := strings.HasPrefix(dataSource, "s3://")
	outputIsS3 := strings.HasPrefix(outputURI, "s3://")
	if dataIsS3 != outputIsS3 {
		return ErrMixedLocalS3
	}
	return nil
}

// SubmitResult checks that a driver submit result has a non-empty ID.
func SubmitResult(r *protocol.SubmitResult) error {
	if r.ID == "" {
		return ErrEmptySubmitID
	}
	return nil
}

// StatusResult checks that a driver status result has required fields.
func StatusResult(r *protocol.StatusResult) error {
	if r.ID == "" {
		return ErrEmptyStatusID
	}
	if r.State == "" {
		return ErrEmptyState
	}
	return nil
}
