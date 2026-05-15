package wrap

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/tomr-ninja/coach/docker"
	"github.com/tomr-ninja/coach/internal/utils"
	"github.com/tomr-ninja/coach/internal/version"
)

var (
	//go:embed templates/entrypoint.template
	entrypointTemplate string

	//go:embed templates/Dockerfile.template
	dockerfileTemplate string
)

// ErrNoRegistry is returned when a registry is required but not configured.
var ErrNoRegistry = errors.New("registry is required for remote S3 runs (set in coach.json)")

// Image builds a wrapper Docker image on top of the base model image.
//
// targetPlatform is an optional "os/arch" specifier (e.g., "linux/amd64").
// When empty, the platform is auto-detected from the locally available base image.
// The coach-sidecar binary is cross-compiled inside a multi-stage Docker build.
// The source code is fetched from GitHub at the commit pinned in version.Commit.
func Image(ctx context.Context, dc *docker.Client, baseImage, registry, registryAuth, targetPlatform string, entrypoint []string) (string, error) {
	safeName := utils.SanitizeImageName(baseImage)
	shortFP := randomSuffix()
	tag := fmt.Sprintf("coach-wrapped-%s:%s", safeName, shortFP)
	if registry != "" {
		tag = registry + "/" + tag
	}

	tmpDir, tmpErr := os.MkdirTemp("", "coach-wrap-*")
	if tmpErr != nil {
		return "", fmt.Errorf("create temp dir: %w", tmpErr)
	}
	defer os.RemoveAll(tmpDir)

	var runCommand string
	if len(entrypoint) > 0 {
		quoted := make([]string, len(entrypoint))
		for i, e := range entrypoint {
			quoted[i] = utils.ShellQuote(e)
		}
		runCommand = fmt.Sprintf("%s \"$@\"", strings.Join(quoted, " "))
	} else {
		runCommand = "\"$@\""
	}

	ep, err := template.New("entrypoint").Parse(entrypointTemplate)
	if err != nil {
		return "", fmt.Errorf("parse entrypoint template: %w", err)
	}
	var entrypointBuf strings.Builder
	if err := ep.Execute(&entrypointBuf, map[string]string{"RunCommand": runCommand}); err != nil {
		return "", fmt.Errorf("render entrypoint: %w", err)
	}

	if tmpErr = os.WriteFile(filepath.Join(tmpDir, "entrypoint.sh"), []byte(entrypointBuf.String()), 0o600); tmpErr != nil {
		return "", fmt.Errorf("write entrypoint.sh: %w", tmpErr)
	}

	// Resolve target platform before rendering Dockerfile.
	if targetPlatform == "" {
		var err error
		targetPlatform, err = dc.ImagePlatform(ctx, baseImage)
		if err != nil {
			return "", fmt.Errorf("get image platform: %w", err)
		}
	} else {
		localPlatform, err := dc.ImagePlatform(ctx, baseImage)
		if err == nil && localPlatform != targetPlatform {
			fmt.Fprintf(os.Stderr, "warning: backend platform is %s, but local base image is %s. "+
				"Docker will pull the %s variant during build. "+
				"If the image doesn't support %s, the build will fail.\n",
				targetPlatform, localPlatform, targetPlatform, targetPlatform)
		}
	}

	targetOS, targetArch := "linux", "amd64"
	if parts := strings.SplitN(targetPlatform, "/", 2); len(parts) == 2 {
		targetOS, targetArch = parts[0], parts[1]
	}
	// Normalize Docker variant to Go GOARCH (e.g. "arm64/v8" -> "arm64").
	if idx := strings.IndexByte(targetArch, '/'); idx >= 0 {
		targetArch = targetArch[:idx]
	}

	tmpl, tmpErr := template.New("Dockerfile").Parse(dockerfileTemplate)
	if tmpErr != nil {
		return "", fmt.Errorf("parse Dockerfile template: %w", tmpErr)
	}
	dockerfile, tmpErr := os.Create(filepath.Join(tmpDir, "Dockerfile"))
	if tmpErr != nil {
		return "", fmt.Errorf("create Dockerfile: %w", tmpErr)
	}
	defer dockerfile.Close()
	if tmpErr = tmpl.Execute(dockerfile, map[string]string{
		"BaseImage":    baseImage,
		"TargetOS":     targetOS,
		"TargetArch":   targetArch,
		"BuilderOS":    runtime.GOOS,
		"BuilderArch":  runtime.GOARCH,
		"CoachVersion": version.Commit,
	}); tmpErr != nil {
		return "", fmt.Errorf("render Dockerfile: %w", tmpErr)
	}
	dockerfile.Close()

	buildCtx, ctxErr := docker.BuildContextDir(tmpDir)
	if ctxErr != nil {
		return "", fmt.Errorf("build context: %w", ctxErr)
	}

	if utils.IsVerbose(ctx) {
		fmt.Fprintf(os.Stderr, "building wrapper image %s...\n", tag)
	}
	if err := dc.ImageBuild(ctx, buildCtx, tag, targetPlatform); err != nil {
		return "", fmt.Errorf("build image: %w", err)
	}

	if registry != "" {
		if utils.IsVerbose(ctx) {
			fmt.Fprintf(os.Stderr, "pushing wrapper image %s...\n", tag)
		}
		if err := dc.ImagePush(ctx, tag, registryAuth); err != nil {
			return "", fmt.Errorf("push image: %w", err)
		}
	}

	return tag, nil
}

// randomSuffix generates a random 12-hex-char suffix for image tag uniqueness.
func randomSuffix() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}
