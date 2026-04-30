package coach

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tomr-ninja/coach/docker"
)

var zeroFingerprint = [32]byte{}

func Run(modelImage, dataDir, outputDir string, force bool) ([32]byte, error) {
	client, err := docker.NewRealDockerClient()
	if err != nil {
		return zeroFingerprint, fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	digest, err := client.ImageDigest(modelImage)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("image digest: %w", err)
	}

	chunkChecksums, err := collectDataChecksums(dataDir)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("data checksums: %w", err)
	}

	fingerprint := artifactFingerprint(digest, chunkChecksums)
	artifactsDir := filepath.Join(outputDir, fmt.Sprintf("%x", fingerprint))

	if info, err := os.Stat(artifactsDir); err == nil && info.IsDir() {
		if !force {
			return zeroFingerprint, fmt.Errorf("artifact %x already exists", fingerprint)
		}
		if err := os.RemoveAll(artifactsDir); err != nil {
			return zeroFingerprint, fmt.Errorf("remove existing artifact: %w", err)
		}
	}

	if err := os.MkdirAll(artifactsDir, 0755); err != nil {
		return zeroFingerprint, fmt.Errorf("create artifact dir: %w", err)
	}

	if err = client.Run(modelImage, dataDir, artifactsDir); err != nil {
		return zeroFingerprint, fmt.Errorf("run model: %w", err)
	}
	if _, err = os.Stat(artifactsDir); err != nil {
		return zeroFingerprint, fmt.Errorf("no artifact created after run")
	}

	return fingerprint, nil
}

func collectDataChecksums(dataDir string) ([][32]byte, error) {
	includeFile := filepath.Join(dataDir, ".coachinclude")
	ignoreFile := filepath.Join(dataDir, ".coachignore")

	useWhitelist := false
	var whitelist map[string]bool
	if _, err := os.Stat(includeFile); err == nil {
		useWhitelist = true
		whitelist = make(map[string]bool)
		f, err := os.Open(includeFile)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				whitelist[line] = true
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}

	var blacklist map[string]bool
	if !useWhitelist {
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

		if rel == ".coachinclude" || rel == ".coachignore" {
			return nil
		}

		if useWhitelist {
			if !whitelist[rel] {
				return nil
			}
		} else if blacklist[rel] {
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

	slices.SortFunc(checksums, func(a, b [32]byte) int {
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

	return checksums, nil
}

func fileSHA256(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return zeroFingerprint, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return zeroFingerprint, err
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

func artifactFingerprint(modelDigest [32]byte, chunkChecksums [][32]byte) [32]byte {
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
