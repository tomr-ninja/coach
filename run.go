package coach

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tomr-ninja/coach/docker"
	"github.com/tomr-ninja/coach/internal/artifact"
	"github.com/tomr-ninja/coach/internal/config"
	"github.com/tomr-ninja/coach/internal/storage"
	"github.com/tomr-ninja/coach/internal/utils"
	"github.com/tomr-ninja/coach/internal/validate"
	"github.com/tomr-ninja/coach/internal/wrap"
)

var (
	errNoDataFiles = errors.New("no data files found")
)

func Run(ctx context.Context, modelImage, dataDir, outputDir string, force bool) ([32]byte, error) {
	if err := validate.ModelImage(modelImage); err != nil {
		return artifact.Zero, fmt.Errorf("validate model image: %w", err)
	}
	if err := validate.DirPath(dataDir); err != nil {
		return artifact.Zero, fmt.Errorf("validate data path: %w", err)
	}
	if err := validate.DirPath(outputDir); err != nil {
		return artifact.Zero, fmt.Errorf("validate output dir: %w", err)
	}

	dataIsS3 := strings.HasPrefix(dataDir, "s3://")
	outputIsS3 := strings.HasPrefix(outputDir, "s3://")
	if dataIsS3 != outputIsS3 {
		return artifact.Zero, validate.ErrMixedLocalS3
	}

	if dataIsS3 && outputIsS3 {
		cfg, err := config.LoadConfig()
		if err != nil {
			return artifact.Zero, fmt.Errorf("load config: %w", err)
		}
		return RunS3(ctx, cfg, modelImage, dataDir, outputDir, force)
	}

	return RunLocal(ctx, modelImage, dataDir, outputDir, force)
}

func RunLocal(ctx context.Context, modelImage, dataDir, outputDir string, force bool) (fp [32]byte, runErr error) {
	if err := validate.ModelImage(modelImage); err != nil {
		return artifact.Zero, fmt.Errorf("validate model image: %w", err)
	}
	if err := validate.DirPath(dataDir); err != nil {
		return artifact.Zero, fmt.Errorf("validate data path: %w", err)
	}
	if err := validate.DirPath(outputDir); err != nil {
		return artifact.Zero, fmt.Errorf("validate output dir: %w", err)
	}

	client, err := docker.NewClient()
	if err != nil {
		return artifact.Zero, fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	if utils.IsVerbose(ctx) {
		fmt.Fprintf(os.Stderr, "resolving checksums...\n")
	}
	chunkChecksums, err := artifact.CollectChecksums(dataDir)
	if err != nil {
		return artifact.Zero, fmt.Errorf("data checksums: %w", err)
	}

	fingerprint, artifactsDir, err := resolveArtifactDir(ctx, client, modelImage, chunkChecksums, outputDir, force)
	if err != nil {
		return artifact.Zero, err
	}

	// Clean up empty artifact directory if the run fails.
	defer func() {
		if runErr == nil {
			return
		}
		entries, readErr := os.ReadDir(artifactsDir)
		if readErr != nil || len(entries) > 0 {
			return
		}
		_ = os.Remove(artifactsDir)
	}()

	if utils.IsVerbose(ctx) {
		fmt.Fprintf(os.Stderr, "running model in container...\n")
	}
	if err := client.Run(ctx, modelImage, dataDir, artifactsDir); err != nil {
		return artifact.Zero, fmt.Errorf("run model: %w", err)
	}
	if err := artifact.ValidateDir(artifactsDir); err != nil {
		return artifact.Zero, err
	}

	return fingerprint, nil
}

func RunS3(ctx context.Context, cfg *config.Config, modelImage, s3DataSource, s3OutputDir string, force bool) ([32]byte, error) {
	if err := validate.ModelImage(modelImage); err != nil {
		return artifact.Zero, fmt.Errorf("validate model image: %w", err)
	}
	if err := validate.DirPath(s3DataSource); err != nil {
		return artifact.Zero, fmt.Errorf("validate data path: %w", err)
	}
	if err := validate.DirPath(s3OutputDir); err != nil {
		return artifact.Zero, fmt.Errorf("validate output dir: %w", err)
	}

	client, err := docker.NewClient()
	if err != nil {
		return artifact.Zero, fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	if utils.IsVerbose(ctx) {
		fmt.Fprintf(os.Stderr, "resolving S3 checksums...\n")
	}
	checksums, err := ResolveChecksums(s3DataSource, cfg)
	if err != nil {
		return artifact.Zero, fmt.Errorf("resolve checksums: %w", err)
	}

	fingerprint, err := resolveFingerprint(ctx, client, modelImage, checksums)
	if err != nil {
		return artifact.Zero, err
	}

	fingerprintHex := fmt.Sprintf("%x", fingerprint)

	s3PathIn := strings.TrimPrefix(s3DataSource, "s3://")
	s3PathOut := strings.TrimPrefix(s3OutputDir, "s3://")
	if !strings.HasSuffix(s3PathOut, "/") {
		s3PathOut += "/"
	}
	s3PathOut += fingerprintHex

	if !force {
		exists, err := storage.ArtifactExists(ctx, cfg, s3PathOut)
		if err != nil {
			return artifact.Zero, fmt.Errorf("check s3 artifact: %w", err)
		}
		if exists {
			return artifact.Zero, fmt.Errorf("%w: s3://%s", artifact.ErrExists, s3PathOut)
		}
	}

	envVars := buildS3ContainerEnv(cfg, s3PathIn, s3PathOut)
	if err := wrapAndRunS3(ctx, client, modelImage, fingerprint, envVars); err != nil {
		return artifact.Zero, err
	}

	return fingerprint, nil
}

// ResolveChecksums resolves data checksums for either a local path or an S3 URI.
func ResolveChecksums(source string, cfg *config.Config) ([][32]byte, error) {
	if strings.HasPrefix(source, "s3://") {
		checksums, err := storage.Checksums(source, cfg)
		if err != nil {
			return nil, fmt.Errorf("s3 checksums for %s: %w", source, err)
		}
		if len(checksums) == 0 {
			return nil, storage.ErrNoObjects
		}
		return checksums, nil
	}

	checksums, err := artifact.CollectChecksums(source)
	if err != nil {
		return nil, fmt.Errorf("local checksums for %s: %w", source, err)
	}
	if len(checksums) == 0 {
		return nil, errNoDataFiles
	}
	return checksums, nil
}

func resolveFingerprint(ctx context.Context, client *docker.Client, modelImage string, checksums [][32]byte) ([32]byte, error) {
	digest, err := client.EnsureImageDigest(ctx, modelImage, "")
	if err != nil {
		return artifact.Zero, fmt.Errorf("image digest: %w", err)
	}
	return artifact.Fingerprint(digest, checksums), nil
}

func resolveArtifactDir(ctx context.Context, client *docker.Client, modelImage string, checksums [][32]byte, outputDir string, force bool) (resultFP [32]byte, resultDir string, err error) {
	fingerprint, err := resolveFingerprint(ctx, client, modelImage, checksums)
	if err != nil {
		return artifact.Zero, "", err
	}

	artifactsDir := filepath.Join(outputDir, fmt.Sprintf("%x", fingerprint))

	if err := artifact.PrepareDir(artifactsDir, force); err != nil {
		return artifact.Zero, "", err
	}

	return fingerprint, artifactsDir, nil
}

func buildS3ContainerEnv(cfg *config.Config, s3PathIn, s3PathOut string) map[string]string {
	envVars := storage.BuildEnvVars(cfg.S3)
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
	wrappedImage, err := wrap.Image(ctx, client, modelImage, fingerprintHex, "", "", "", entrypoint)
	if err != nil {
		return fmt.Errorf("wrap image: %w", err)
	}

	if err = client.RunWrapped(ctx, wrappedImage, envVars); err != nil {
		return fmt.Errorf("run wrapped model: %w", err)
	}

	return nil
}
