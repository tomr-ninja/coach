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

func Run(ctx context.Context, modelImage, dataDir, outputDir string, force bool) ([32]byte, error) {
	if err := ValidateModelImage(modelImage); err != nil {
		return zeroFingerprint, fmt.Errorf("validate model image: %w", err)
	}
	if err := ValidateDataPath(dataDir); err != nil {
		return zeroFingerprint, fmt.Errorf("validate data path: %w", err)
	}
	if err := ValidateOutputDir(outputDir); err != nil {
		return zeroFingerprint, fmt.Errorf("validate output dir: %w", err)
	}

	dataIsS3 := strings.HasPrefix(dataDir, "s3://")
	outputIsS3 := strings.HasPrefix(outputDir, "s3://")
	if dataIsS3 != outputIsS3 {
		return zeroFingerprint, errMixedLocalS3
	}

	if dataIsS3 && outputIsS3 {
		cfg, err := LoadConfig()
		if err != nil {
			return zeroFingerprint, fmt.Errorf("load config: %w", err)
		}
		return RunS3(ctx, cfg, modelImage, dataDir, outputDir, force)
	}

	return RunLocal(ctx, modelImage, dataDir, outputDir, force)
}

func RunLocal(ctx context.Context, modelImage, dataDir, outputDir string, force bool) ([32]byte, error) {
	if err := ValidateModelImage(modelImage); err != nil {
		return zeroFingerprint, fmt.Errorf("validate model image: %w", err)
	}
	if err := ValidateDataPath(dataDir); err != nil {
		return zeroFingerprint, fmt.Errorf("validate data path: %w", err)
	}
	if err := ValidateOutputDir(outputDir); err != nil {
		return zeroFingerprint, fmt.Errorf("validate output dir: %w", err)
	}

	client, err := docker.NewRealDockerClient()
	if err != nil {
		return zeroFingerprint, fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	chunkChecksums, err := CollectDataChecksums(dataDir)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("data checksums: %w", err)
	}

	fingerprint, artifactsDir, err := resolveArtifactDir(ctx, client, modelImage, chunkChecksums, outputDir, force)
	if err != nil {
		return zeroFingerprint, err
	}

	if err := client.Run(ctx, modelImage, dataDir, artifactsDir); err != nil {
		return zeroFingerprint, fmt.Errorf("run model: %w", err)
	}
	if err := validateArtifactDir(artifactsDir); err != nil {
		return zeroFingerprint, err
	}

	return fingerprint, nil
}

func RunS3(ctx context.Context, cfg *Config, modelImage, s3DataSource, s3OutputDir string, force bool) ([32]byte, error) {
	if err := ValidateModelImage(modelImage); err != nil {
		return zeroFingerprint, fmt.Errorf("validate model image: %w", err)
	}
	if err := ValidateDataPath(s3DataSource); err != nil {
		return zeroFingerprint, fmt.Errorf("validate data path: %w", err)
	}
	if err := ValidateOutputDir(s3OutputDir); err != nil {
		return zeroFingerprint, fmt.Errorf("validate output dir: %w", err)
	}

	client, err := docker.NewRealDockerClient()
	if err != nil {
		return zeroFingerprint, fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	checksums, err := ResolveChecksums(s3DataSource, cfg)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("resolve checksums: %w", err)
	}

	fingerprint, err := resolveFingerprint(ctx, client, modelImage, checksums)
	if err != nil {
		return zeroFingerprint, err
	}

	fingerprintHex := fmt.Sprintf("%x", fingerprint)

	s3PathIn := strings.TrimPrefix(s3DataSource, "s3://")
	s3PathOut := strings.TrimPrefix(s3OutputDir, "s3://")
	if !strings.HasSuffix(s3PathOut, "/") {
		s3PathOut += "/"
	}
	s3PathOut += fingerprintHex

	if !force {
		exists, err := s3ArtifactExists(ctx, cfg, s3PathOut)
		if err != nil {
			return zeroFingerprint, fmt.Errorf("check s3 artifact: %w", err)
		}
		if exists {
			return zeroFingerprint, fmt.Errorf("%w: s3://%s", errArtifactExists, s3PathOut)
		}
	}

	envVars := buildS3ContainerEnv(cfg, s3PathIn, s3PathOut)
	if err := wrapAndRunS3(ctx, client, modelImage, fingerprint, envVars); err != nil {
		return zeroFingerprint, err
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

	return checksums, nil
}

func resolveFingerprint(ctx context.Context, client *docker.Client, modelImage string, checksums [][32]byte) ([32]byte, error) {
	digest, err := client.ImageDigest(ctx, modelImage)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("image digest: %w", err)
	}
	return artifactFingerprint(digest, checksums), nil
}

func resolveArtifactDir(ctx context.Context, client *docker.Client, modelImage string, checksums [][32]byte, outputDir string, force bool) (resultFP [32]byte, resultDir string, err error) {
	fingerprint, err := resolveFingerprint(ctx, client, modelImage, checksums)
	if err != nil {
		return zeroFingerprint, "", err
	}

	artifactsDir := filepath.Join(outputDir, fmt.Sprintf("%x", fingerprint))

	if err := prepareArtifactDir(artifactsDir, force); err != nil {
		return zeroFingerprint, "", err
	}

	return fingerprint, artifactsDir, nil
}

func buildS3ContainerEnv(cfg *Config, s3PathIn, s3PathOut string) map[string]string {
	envVars := buildS3EnvVars(cfg.S3)
	envVars["S3_PATH_IN"] = s3PathIn
	envVars["S3_PATH_OUT"] = s3PathOut

	return envVars
}

func wrapAndRunS3(ctx context.Context, client *docker.Client, modelImage string, fingerprint [32]byte, envVars map[string]string) error {
	entrypoint, _, err := client.ImageEntrypoint(ctx, modelImage)
	if err != nil {
		return fmt.Errorf("inspect image entrypoint: %w", err)
	}

	fingerprintHex := fmt.Sprintf("%x", fingerprint)
	wrappedImage, err := WrapImage(ctx, client, modelImage, fingerprintHex, "", "", entrypoint)
	if err != nil {
		return fmt.Errorf("wrap image: %w", err)
	}

	if err = client.RunWrapped(ctx, wrappedImage, envVars); err != nil {
		return fmt.Errorf("run wrapped model: %w", err)
	}

	return nil
}

func prepareArtifactDir(path string, force bool) error {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		if !force {
			return fmt.Errorf("%w: %s", errArtifactExists, path)
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

func validateArtifactDir(path string) error {
	if _, err := os.Stat(path); err != nil {
		return errArtifactMissing
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read artifact dir: %w", err)
	}
	if len(entries) == 0 {
		return errArtifactEmpty
	}
	return nil
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
