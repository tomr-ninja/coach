package coach

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tomr-ninja/coach/docker"
	"github.com/tomr-ninja/coach/internal/artifact"
	"github.com/tomr-ninja/coach/internal/config"
	"github.com/tomr-ninja/coach/internal/utils"
	"github.com/tomr-ninja/coach/internal/validate"
	"github.com/tomr-ninja/coach/internal/wrap"
)

func Run(ctx context.Context, cfg *config.Config, modelImage, dataDir, outputDir string, force bool) ([32]byte, error) {
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

	fingerprint, s3PathOutPrefix, err := resolveS3ArtifactFingerprint(ctx, imageDigestHex, s3DataSource, s3OutputDir, cfg, force)
	if err != nil {
		return artifact.Zero, err
	}

	s3PathIn := strings.TrimPrefix(s3DataSource, "s3://")

	envVars := s3WrapperEnvVars(cfg, modelImage, s3PathIn, s3PathOutPrefix, imageDigestHex)
	if err := wrapAndRunS3(ctx, client, modelImage, envVars); err != nil {
		return artifact.Zero, err
	}

	return fingerprint, nil
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
func s3WrapperEnvVars(cfg *config.Config, modelImage, s3PathIn, s3PathOutPrefix, imageDigestHex string) map[string]string {
	s3 := cfg.S3
	envVars := map[string]string{
		"COACH_MODEL_IMAGE":     modelImage,
		"S3_PATH_IN":            s3PathIn,
		"S3_PATH_OUT_PREFIX":    s3PathOutPrefix,
		"COACH_IMAGE_DIGEST":    imageDigestHex,
		"COACH_WEBHOOK_URL":     cfg.WebhookURL,
		"AWS_ACCESS_KEY_ID":     s3.AccessKeyID.Reveal(),
		"AWS_SECRET_ACCESS_KEY": s3.SecretAccessKey.Reveal(),
		"AWS_REGION":            s3.Region,
	}
	if s3.Endpoint != "" {
		envVars["AWS_ENDPOINT_URL"] = s3.Endpoint
	}
	return envVars
}

func wrapAndRunS3(ctx context.Context, client *docker.Client, modelImage string, envVars map[string]string) error {
	entrypoint, _, err := client.ImageEntrypoint(ctx, modelImage)
	if err != nil {
		return fmt.Errorf("inspect image entrypoint: %w", err)
	}

	wrappedImage, err := wrap.Image(ctx, client, modelImage, "", "", "", entrypoint)
	if err != nil {
		return fmt.Errorf("wrap image: %w", err)
	}

	if err = client.RunWrapped(ctx, wrappedImage, envVars); err != nil {
		return fmt.Errorf("run wrapped model: %w", err)
	}

	return nil
}
