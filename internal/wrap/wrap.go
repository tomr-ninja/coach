package wrap

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
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
// The coach-artifact binary is cross-compiled inside a multi-stage Docker build.
func Image(ctx context.Context, dc *docker.Client, baseImage, _, registry, registryAuth, targetPlatform string, entrypoint []string) (string, error) {
	safeName := SanitizeImageName(baseImage)
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

	// Copy Go source files needed by the multi-stage Docker build.
	moduleRoot, err := moduleRootDir()
	if err != nil {
		return "", fmt.Errorf("find module root: %w", err)
	}
	if err := copyFile(filepath.Join(moduleRoot, "go.mod"), filepath.Join(tmpDir, "go.mod")); err != nil {
		return "", fmt.Errorf("copy go.mod: %w", err)
	}
	if err := copyFile(filepath.Join(moduleRoot, "go.sum"), filepath.Join(tmpDir, "go.sum")); err != nil {
		return "", fmt.Errorf("copy go.sum: %w", err)
	}
	if err := copyDir(filepath.Join(moduleRoot, "internal", "artifact"), filepath.Join(tmpDir, "internal", "artifact")); err != nil {
		return "", fmt.Errorf("copy internal/artifact: %w", err)
	}
	if err := copyDir(filepath.Join(moduleRoot, "cmd", "coach-artifact"), filepath.Join(tmpDir, "cmd", "coach-artifact")); err != nil {
		return "", fmt.Errorf("copy cmd/coach-artifact: %w", err)
	}

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
	if tmpErr = tmpl.Execute(dockerfile, map[string]string{"BaseImage": baseImage, "TargetOS": targetOS, "TargetArch": targetArch}); tmpErr != nil {
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

// randomSuffix generates a random 12-hex-char suffix for image tag uniqueness.
func randomSuffix() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// moduleRootDir finds the Go module root directory by walking up from the
// current directory until go.mod is found.
func moduleRootDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found in any parent directory")
		}
		dir = parent
	}
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()

	_, err = io.Copy(d, s)
	return err
}

func copyDir(srcRoot, dstRoot string) error {
	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		return copyFile(path, dst)
	})
}
