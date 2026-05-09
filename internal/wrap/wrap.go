package wrap

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/tomr-ninja/coach/docker"
	"github.com/tomr-ninja/coach/internal/utils"
)

var (
	//go:embed templates/entrypoint.template
	entrypointTemplate string

	//go:embed templates/Dockerfile.template
	dockerfileTemplate string
)

// ErrNoRegistry is returned when a registry is required but not configured.
var ErrNoRegistry = fmt.Errorf("registry is required for remote S3 runs (set in coach.json)")

// Image builds a wrapper Docker image on top of the base model image.
//
// targetPlatform is an optional "os/arch" specifier (e.g., "linux/amd64").
// When empty, the platform is auto-detected from the locally available base image.
func Image(ctx context.Context, dc *docker.Client, baseImage, fingerprint, registry, registryAuth, targetPlatform string, entrypoint []string) (string, error) {
	safeName := SanitizeImageName(baseImage)
	shortFP := fingerprint
	if len(shortFP) > 12 {
		shortFP = shortFP[:12]
	}
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
			quoted[i] = ShellQuote(e)
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

	tmpl, tmpErr := template.New("Dockerfile").Parse(dockerfileTemplate)
	if tmpErr != nil {
		return "", fmt.Errorf("parse Dockerfile template: %w", tmpErr)
	}
	dockerfile, tmpErr := os.Create(filepath.Join(tmpDir, "Dockerfile"))
	if tmpErr != nil {
		return "", fmt.Errorf("create Dockerfile: %w", tmpErr)
	}
	defer dockerfile.Close()
	if tmpErr = tmpl.Execute(dockerfile, map[string]string{"BaseImage": baseImage}); tmpErr != nil {
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

var safeImageRegexp = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// SanitizeImageName converts a Docker image reference into a safe tag fragment.
func SanitizeImageName(name string) string {
	parts := strings.Split(name, "/")
	last := parts[len(parts)-1]
	last = strings.ReplaceAll(last, ":", "-")
	return safeImageRegexp.ReplaceAllString(last, "-")
}

// ShellQuote escapes a string for safe use in a POSIX shell.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if !shellSafe(r) {
			return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
		}
	}
	return s
}

func shellSafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	}
	switch r {
	case '-', '_', '.', '/', ',':
		return true
	}
	return false
}
