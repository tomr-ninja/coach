package artifact

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var (
	ErrExists  = errors.New("artifact already exists")
	ErrEmpty   = errors.New("model ran but produced no output")
	ErrMissing = errors.New("artifact directory was not created")
	Zero       = [32]byte{}
)

// CollectChecksums walks a local data directory and computes SHA256
// checksums for every file. Files listed in .coachignore are skipped.
func CollectChecksums(dataDir string) ([][32]byte, error) {
	ignoreFile := filepath.Join(dataDir, ".coachignore")

	var blacklist map[string]bool
	if _, err := os.Stat(ignoreFile); err == nil {
		blacklist = make(map[string]bool)
		f, err := os.Open(ignoreFile)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				blacklist[line] = true
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}

	var checksums [][32]byte
	err := filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}

		if rel == ".coachignore" {
			return nil
		}

		if blacklist[rel] {
			return nil
		}

		h, err := fileSHA256(path)
		if err != nil {
			return err
		}
		checksums = append(checksums, h)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return checksums, nil
}

// Fingerprint combines a model digest with sorted data chunk checksums
// into a single SHA256 artifact fingerprint.
func Fingerprint(modelDigest [32]byte, chunkChecksums [][32]byte) [32]byte {
	sorted := slices.Clone(chunkChecksums)
	slices.SortFunc(sorted, func(a, b [32]byte) int {
		for i := range a {
			if a[i] < b[i] {
				return -1
			}
			if a[i] > b[i] {
				return 1
			}
		}
		return 0
	})

	h := sha256.New()
	h.Write(modelDigest[:])
	for _, ch := range sorted {
		h.Write(ch[:])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))

	return out
}

// PrepareDir creates the artifact directory, removing any existing one
// if force is true.
func PrepareDir(path string, force bool) error {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		if !force {
			return fmt.Errorf("%w: %s", ErrExists, path)
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove existing artifact: %w", err)
		}
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create artifact dir: %w", err)
	}
	return nil
}

// ValidateDir checks that the artifact directory exists and is non-empty.
func ValidateDir(path string) error {
	if _, err := os.Stat(path); err != nil {
		return ErrMissing
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read artifact dir: %w", err)
	}
	if len(entries) == 0 {
		return ErrEmpty
	}
	return nil
}

func fileSHA256(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return Zero, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return Zero, err
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}
