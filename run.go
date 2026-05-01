package coach

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tomr-ninja/coach/docker"
)

var (
	zeroFingerprint    = [32]byte{}
	errArtifactExists  = errors.New("artifact already exists")
	errArtifactEmpty   = errors.New("model ran but produced no output")
	errArtifactMissing = errors.New("artifact directory was not created")
)

func Run(modelImage, dataDir, outputDir string, force bool) ([32]byte, error) {
	if strings.HasPrefix(dataDir, "s3://") {
		cfg, err := LoadConfig()
		if err != nil {
			return zeroFingerprint, fmt.Errorf("load config: %w", err)
		}
		return RunS3(cfg, modelImage, dataDir, outputDir, force)
	}
	return RunLocal(modelImage, dataDir, outputDir, force)
}

func RunLocal(modelImage, dataDir, outputDir string, force bool) ([32]byte, error) {
	client, err := docker.NewRealDockerClient()
	if err != nil {
		return zeroFingerprint, fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	digest, err := client.ImageDigest(modelImage)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("image digest: %w", err)
	}

	chunkChecksums, err := CollectDataChecksums(dataDir)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("data checksums: %w", err)
	}

	fingerprint := artifactFingerprint(digest, chunkChecksums)
	artifactsDir := filepath.Join(outputDir, fmt.Sprintf("%x", fingerprint))

	if info, statErr := os.Stat(artifactsDir); statErr == nil && info.IsDir() {
		if !force {
			return zeroFingerprint, fmt.Errorf("%w: %x", errArtifactExists, fingerprint)
		}
		if removeErr := os.RemoveAll(artifactsDir); removeErr != nil {
			return zeroFingerprint, fmt.Errorf("remove existing artifact: %w", removeErr)
		}
	}

	if mkdirErr := os.MkdirAll(artifactsDir, 0o755); mkdirErr != nil {
		return zeroFingerprint, fmt.Errorf("create artifact dir: %w", mkdirErr)
	}

	if err = client.Run(modelImage, dataDir, artifactsDir); err != nil {
		return zeroFingerprint, fmt.Errorf("run model: %w", err)
	}
	if _, statErr := os.Stat(artifactsDir); statErr != nil {
		return zeroFingerprint, errArtifactMissing
	}
	entries, err := os.ReadDir(artifactsDir)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("read artifact dir: %w", err)
	}
	if len(entries) == 0 {
		return zeroFingerprint, errArtifactEmpty
	}

	return fingerprint, nil
}

func RunS3(cfg *Config, modelImage, s3DataSource, localOutputDir string, force bool) ([32]byte, error) {
	client, err := docker.NewRealDockerClient()
	if err != nil {
		return zeroFingerprint, fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	digest, err := client.ImageDigest(modelImage)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("image digest: %w", err)
	}

	checksums, err := ResolveChecksums(s3DataSource, cfg)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("resolve checksums: %w", err)
	}

	fingerprint := artifactFingerprint(digest, checksums)
	fingerprintHex := fmt.Sprintf("%x", fingerprint)
	artifactsDir := filepath.Join(localOutputDir, fingerprintHex)

	if info, statErr := os.Stat(artifactsDir); statErr == nil && info.IsDir() {
		if !force {
			return zeroFingerprint, fmt.Errorf("%w: %x", errArtifactExists, fingerprint)
		}
		if removeErr := os.RemoveAll(artifactsDir); removeErr != nil {
			return zeroFingerprint, fmt.Errorf("remove existing artifact: %w", removeErr)
		}
	}

	if mkdirErr := os.MkdirAll(artifactsDir, 0o755); mkdirErr != nil {
		return zeroFingerprint, fmt.Errorf("create artifact dir: %w", mkdirErr)
	}

	entrypoint, _, entryErr := client.ImageEntrypoint(modelImage)
	if entryErr != nil {
		return zeroFingerprint, fmt.Errorf("inspect image entrypoint: %w", entryErr)
	}

	s3PathIn := strings.TrimPrefix(s3DataSource, "s3://")
	s3PathOutAbs := filepath.Join(artifactsDir, "output")

	envVars := map[string]string{
		"S3_PATH_IN":  s3PathIn,
		"S3_PATH_OUT": s3PathOutAbs,
	}
	envVars["RCLONE_CONFIG_S3-STORAGE_TYPE"] = "s3"
	if cfg.S3.Provider != "" {
		envVars["RCLONE_CONFIG_S3-STORAGE_PROVIDER"] = cfg.S3.Provider
	} else {
		envVars["RCLONE_CONFIG_S3-STORAGE_PROVIDER"] = "AWS"
	}
	if cfg.S3.AccessKeyID != "" {
		envVars["RCLONE_CONFIG_S3-STORAGE_ACCESS_KEY_ID"] = cfg.S3.AccessKeyID
	}
	if cfg.S3.SecretAccessKey != "" {
		envVars["RCLONE_CONFIG_S3-STORAGE_SECRET_ACCESS_KEY"] = cfg.S3.SecretAccessKey
	}
	if cfg.S3.Region != "" {
		envVars["RCLONE_CONFIG_S3-STORAGE_REGION"] = cfg.S3.Region
	}
	if cfg.S3.Endpoint != "" {
		envVars["RCLONE_CONFIG_S3-STORAGE_ENDPOINT"] = cfg.S3.Endpoint
	}

	wrappedImage, wrapErr := WrapImage(context.Background(), client, modelImage, fingerprintHex, "", entrypoint)
	if wrapErr != nil {
		return zeroFingerprint, fmt.Errorf("wrap image: %w", wrapErr)
	}

	if runErr := client.RunWrapped(wrappedImage, envVars); runErr != nil {
		return zeroFingerprint, fmt.Errorf("run wrapped model: %w", runErr)
	}

	if _, statErr := os.Stat(artifactsDir); statErr != nil {
		return zeroFingerprint, errArtifactMissing
	}
	entries, err := os.ReadDir(artifactsDir)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("read artifact dir: %w", err)
	}
	if len(entries) == 0 {
		return zeroFingerprint, errArtifactEmpty
	}

	return fingerprint, nil
}

func CollectDataChecksums(dataDir string) ([][32]byte, error) {
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
