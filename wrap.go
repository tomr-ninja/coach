package coach

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
)

//go:embed templates/entrypoint.template
var entrypointTemplate string

//go:embed templates/Dockerfile.template
var dockerfileTemplate string

func WrapImage(ctx context.Context, dc *docker.Client, baseImage, fingerprint, registry, registryAuth string, entrypoint []string) (string, error) {
	safeName := sanitizeImageName(baseImage)
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
			quoted[i] = shellQuote(e)
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

	if IsVerbose(ctx) {
		fmt.Fprintf(os.Stderr, "building wrapper image %s...\n", tag)
	}
	if err := dc.ImageBuild(ctx, buildCtx, tag); err != nil {
		return "", fmt.Errorf("build image: %w", err)
	}

	if registry != "" {
		if IsVerbose(ctx) {
			fmt.Fprintf(os.Stderr, "pushing wrapper image %s...\n", tag)
		}
		if err := dc.ImagePush(ctx, tag, registryAuth); err != nil {
			return "", fmt.Errorf("push image: %w", err)
		}
	}

	return tag, nil
}

var safeImageRegexp = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

func sanitizeImageName(name string) string {
	parts := strings.Split(name, "/")
	last := parts[len(parts)-1]
	last = strings.ReplaceAll(last, ":", "-")
	return safeImageRegexp.ReplaceAllString(last, "-")
}

func shellQuote(s string) string {
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
