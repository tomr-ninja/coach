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

	imageDigestHex, err := resolveImageDigestHex(ctx, client, modelImage)
	if err != nil {
		return artifact.Zero, fmt.Errorf("resolve image digest: %w", err)
	}

	s3PathIn := strings.TrimPrefix(s3DataSource, "s3://")
	s3PathOutPrefix := strings.TrimPrefix(s3OutputDir, "s3://")
	if !strings.HasSuffix(s3PathOutPrefix, "/") {
		s3PathOutPrefix += "/"
	}

	envVars := s3WrapperEnvVars(cfg, s3PathIn, s3PathOutPrefix, imageDigestHex)
	if err := wrapAndRunS3(ctx, client, modelImage, imageDigestHex, envVars); err != nil {
		return artifact.Zero, err
	}

	return artifact.Zero, nil
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

// s3WrapperEnvVars builds env vars for an S3 wrapper container.
func s3WrapperEnvVars(cfg *config.Config, s3PathIn, s3PathOutPrefix, imageDigestHex string) map[string]string {
	s3 := cfg.S3
	envVars := map[string]string{
		"S3_PATH_IN":            s3PathIn,
		"S3_PATH_OUT_PREFIX":    s3PathOutPrefix,
		"COACH_IMAGE_DIGEST":    imageDigestHex,
		"AWS_ACCESS_KEY_ID":     s3.AccessKeyID.Reveal(),
		"AWS_SECRET_ACCESS_KEY": s3.SecretAccessKey.Reveal(),
		"AWS_REGION":            s3.Region,
	}
	if s3.Endpoint != "" {
		envVars["AWS_ENDPOINT_URL"] = s3.Endpoint
	}
	return envVars
}

func wrapAndRunS3(ctx context.Context, client *docker.Client, modelImage, imageDigestHex string, envVars map[string]string) error {
	entrypoint, _, err := client.ImageEntrypoint(ctx, modelImage)
	if err != nil {
		return fmt.Errorf("inspect image entrypoint: %w", err)
	}

	wrappedImage, err := wrap.Image(ctx, client, modelImage, imageDigestHex, "", "", "", entrypoint)
	if err != nil {
		return fmt.Errorf("wrap image: %w", err)
	}

	if err = client.RunWrapped(ctx, wrappedImage, envVars); err != nil {
		return fmt.Errorf("run wrapped model: %w", err)
	}

	return nil
}
