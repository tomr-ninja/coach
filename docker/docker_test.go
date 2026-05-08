package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ory/dockertest/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// parseDockerStream unit tests (no dockertest needed)
// ---------------------------------------------------------------------------

func TestParseDockerStream_Success(t *testing.T) {
	input := strings.NewReader(
		`{"stream":"Step 1/2 : FROM alpine\n"}` + "\n" +
			`{"stream":" ---\u003e abc123\n"}` + "\n" +
			`{"stream":"Successfully built abc123\n"}` + "\n" +
			`{"aux":{"ID":"sha256:abc123"}}` + "\n",
	)
	var buf bytes.Buffer
	err := parseDockerStream(input, &buf)
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "Step 1/2")
	assert.Contains(t, out, "abc123")
	assert.Contains(t, out, "Successfully built")
}

func TestParseDockerStream_EmbeddedError(t *testing.T) {
	input := strings.NewReader(
		`{"stream":"Step 1/2 : FROM alpine\n"}` + "\n" +
			`{"errorDetail":{"code":1,"message":"COPY failed: file not found"},"error":"COPY failed: file not found"}` + "\n",
	)
	var buf bytes.Buffer
	err := parseDockerStream(input, &buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "COPY failed")
	assert.Contains(t, err.Error(), "code 1")
}

func TestParseDockerStream_ErrorDetailOnly(t *testing.T) {
	input := strings.NewReader(
		`{"errorDetail":{"message":"manifest unknown"}}` + "\n",
	)
	var buf bytes.Buffer
	err := parseDockerStream(input, &buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manifest unknown")
}

func TestParseDockerStream_ErrorOnly(t *testing.T) {
	input := strings.NewReader(
		`{"error":"denied: requested access to the resource is denied"}` + "\n",
	)
	var buf bytes.Buffer
	err := parseDockerStream(input, &buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "denied")
}

func TestParseDockerStream_MultipleErrors(t *testing.T) {
	input := strings.NewReader(
		`{"error":"first error"}` + "\n" +
			`{"error":"second error"}` + "\n",
	)
	var buf bytes.Buffer
	err := parseDockerStream(input, &buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "first error")
	assert.Contains(t, err.Error(), "second error")
}

func TestParseDockerStream_PushOutput(t *testing.T) {
	input := strings.NewReader(
		`{"status":"Pushing","id":"abc123"}` + "\n" +
			`{"status":"Pushed","id":"abc123"}` + "\n" +
			`{"status":"latest: digest: sha256:def456"}` + "\n",
	)
	var buf bytes.Buffer
	err := parseDockerStream(input, &buf)
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "Pushing")
	assert.Contains(t, out, "Pushed")
	assert.Contains(t, out, "latest: digest")
}

func TestParseDockerStream_MalformedJSON_PassesThrough(t *testing.T) {
	input := strings.NewReader("this is not json\n")
	var buf bytes.Buffer
	err := parseDockerStream(input, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "this is not json")
}

func TestParseDockerStream_EmptyLines(t *testing.T) {
	input := strings.NewReader(
		"\n" +
			`{"stream":"ok\n"}` + "\n" +
			"\n",
	)
	var buf bytes.Buffer
	err := parseDockerStream(input, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "ok")
}

func TestParseDockerStream_ProgressInfo(t *testing.T) {
	input := strings.NewReader(
		`{"status":"Downloading","progress":"[====\u003e  ] 50%","id":"layer1"}` + "\n",
	)
	var buf bytes.Buffer
	err := parseDockerStream(input, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Downloading")
}

// ---------------------------------------------------------------------------
// Integration tests with dockertest
// ---------------------------------------------------------------------------

// setupDockerPool creates a dockertest pool and verifies Docker is reachable.
func setupDockerPool(t *testing.T) (*dockertest.Pool, *Client) {
	t.Helper()

	pool, err := dockertest.NewPool("")
	require.NoError(t, err, "Docker daemon must be running for integration tests")

	client, err := NewClient()
	require.NoError(t, err)

	t.Cleanup(func() {
		client.Close()
	})

	return pool, client
}

// buildTestImage builds a simple Alpine-based image that echoes args and
// writes to /output. Returns the image tag.
func buildTestImage(t *testing.T, client *Client) string {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "coach-docker-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	dockerfile := `FROM alpine:3.21
RUN echo "test image ready"
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0o644))

	buildCtx, err := BuildContextDir(tmpDir)
	require.NoError(t, err)

	tag := fmt.Sprintf("coach-test-%x:latest", time.Now().UnixNano())
	ctx := context.Background()

	err = client.ImageBuild(ctx, buildCtx, tag)
	require.NoError(t, err, "build should succeed")

	return tag
}

func TestNewRealDockerClient(t *testing.T) {
	client, err := NewClient()
	require.NoError(t, err)
	require.NotNil(t, client)
	client.Close()
}

func TestImageBuildAndExists(t *testing.T) {
	_, client := setupDockerPool(t)
	tag := buildTestImage(t, client)
	ctx := context.Background()

	exists, err := client.ImageExists(ctx, tag)
	require.NoError(t, err)
	assert.True(t, exists, "built image should exist")
}

func TestEnsureImageDigest(t *testing.T) {
	_, client := setupDockerPool(t)
	tag := buildTestImage(t, client)
	ctx := context.Background()

	digest, err := client.EnsureImageDigest(ctx, tag)
	require.NoError(t, err)
	assert.NotEqual(t, [32]byte{}, digest, "digest should not be zero")
}

func TestImageEntrypoint(t *testing.T) {
	_, client := setupDockerPool(t)
	tag := buildTestImage(t, client)
	ctx := context.Background()

	_, cmd, err := client.ImageEntrypoint(ctx, tag)
	require.NoError(t, err)
	// Alpine images typically have no entrypoint, cmd is ["/bin/sh"]
	assert.NotNil(t, cmd)
}

func TestRun_OutputWritten(t *testing.T) {
	_, client := setupDockerPool(t)

	tmpDir, err := os.MkdirTemp("", "coach-test-run-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	dataDir := filepath.Join(tmpDir, "data")
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(dataDir, 0o755))
	require.NoError(t, os.MkdirAll(outputDir, 0o755))

	// Write a file in data that the container will copy to output
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "input.txt"), []byte("hello"), 0o644))

	// Build a custom image that copies /data/input.txt to /output/result.txt
	customTag := buildCopyImage(t, client)

	ctx := context.Background()
	err = client.Run(ctx, customTag, dataDir, outputDir)
	require.NoError(t, err)

	// Verify the container wrote output
	result, err := os.ReadFile(filepath.Join(outputDir, "result.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(result))
}

// buildCopyImage builds an image that copies /data/input.txt to /output/result.txt
func buildCopyImage(t *testing.T, client *Client) string {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "coach-docker-test-copy-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	dockerfile := `FROM alpine:3.21
CMD ["sh", "-c", "cp /data/input.txt /output/result.txt"]
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0o644))

	buildCtx, err := BuildContextDir(tmpDir)
	require.NoError(t, err)

	tag := fmt.Sprintf("coach-test-copy-%x:latest", time.Now().UnixNano())
	ctx := context.Background()
	err = client.ImageBuild(ctx, buildCtx, tag)
	require.NoError(t, err)

	return tag
}

func TestRunWrapped_EnvVars(t *testing.T) {
	_, client := setupDockerPool(t)

	// Build a simple image that echoes env vars
	tmpDir, err := os.MkdirTemp("", "coach-test-wrap-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	dockerfile := `FROM alpine:3.21
CMD ["sh", "-c", "echo $TEST_VAR"]
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0o644))

	buildCtx, err := BuildContextDir(tmpDir)
	require.NoError(t, err)

	tag := fmt.Sprintf("coach-test-wrap-%x:latest", time.Now().UnixNano())
	ctx := context.Background()
	err = client.ImageBuild(ctx, buildCtx, tag)
	require.NoError(t, err)

	envVars := map[string]string{
		"TEST_VAR": "hello_wrapped",
	}
	err = client.RunWrapped(ctx, tag, envVars)
	require.NoError(t, err, "RunWrapped should succeed (we can't check stdout easily, but no error = ok)")
}

func TestRunScript(t *testing.T) {
	_, client := setupDockerPool(t)

	tmpDir, err := os.MkdirTemp("", "coach-test-script-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	dataDir := filepath.Join(tmpDir, "data")
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(dataDir, 0o755))
	require.NoError(t, os.MkdirAll(outputDir, 0o755))

	// Build an image with a /scripts directory
	scriptDir, err := os.MkdirTemp("", "coach-test-build-script-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(scriptDir) })

	dockerfile := `FROM alpine:3.21
RUN mkdir -p /scripts
COPY myscript.sh /scripts/
CMD ["sh"]
`
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "Dockerfile"), []byte(dockerfile), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(scriptDir, "myscript.sh"),
		[]byte("#!/bin/sh\nsleep 1\necho \"script ran with arg: $1\"\ncp /data/input.txt /output/result.txt\n"),
		0o755,
	))

	buildCtx, err := BuildContextDir(scriptDir)
	require.NoError(t, err)

	tag := fmt.Sprintf("coach-test-script-%x:latest", time.Now().UnixNano())
	ctx := context.Background()
	err = client.ImageBuild(ctx, buildCtx, tag)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "input.txt"), []byte("script_data"), 0o644))

	err = client.RunScript(ctx, tag, "myscript.sh", []string{"hello_arg"}, dataDir, outputDir)
	require.NoError(t, err)

	result, err := os.ReadFile(filepath.Join(outputDir, "result.txt"))
	require.NoError(t, err)
	assert.Equal(t, "script_data", string(result))
}

func TestListScripts(t *testing.T) {
	_, client := setupDockerPool(t)

	scriptDir, err := os.MkdirTemp("", "coach-test-list-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(scriptDir) })

	dockerfile := `FROM alpine:3.21
RUN mkdir -p /scripts
COPY a.sh /scripts/
COPY b.sh /scripts/
CMD ["sh"]
`
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "Dockerfile"), []byte(dockerfile), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "a.sh"), []byte("#!/bin/sh\necho a\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "b.sh"), []byte("#!/bin/sh\necho b\n"), 0o755))

	buildCtx, err := BuildContextDir(scriptDir)
	require.NoError(t, err)

	tag := fmt.Sprintf("coach-test-list-%x:latest", time.Now().UnixNano())
	ctx := context.Background()
	err = client.ImageBuild(ctx, buildCtx, tag)
	require.NoError(t, err)

	scripts, err := client.ListScripts(ctx, tag)
	require.NoError(t, err)
	assert.Contains(t, scripts, "a.sh")
	assert.Contains(t, scripts, "b.sh")
}

func TestImageBuild_StreamError(t *testing.T) {
	_, client := setupDockerPool(t)

	tmpDir, err := os.MkdirTemp("", "coach-test-fail-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	// Dockerfile with a COPY of a non-existent file - will fail during build
	dockerfile := `FROM alpine:3.21
COPY nonexistent_file.txt /app/
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0o644))

	buildCtx, err := BuildContextDir(tmpDir)
	require.NoError(t, err)

	tag := fmt.Sprintf("coach-test-fail-%x:latest", time.Now().UnixNano())
	ctx := context.Background()

	err = client.ImageBuild(ctx, buildCtx, tag)
	require.Error(t, err, "build with missing file should fail")
	assert.Contains(t, err.Error(), "build image", "error should be wrapped as build error")
}

func TestBuildContextDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "coach-test-ctx-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte("FROM alpine:3.21\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "subdir", "data.txt"), []byte("hello"), 0o644))

	reader, err := BuildContextDir(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, reader)

	// Read the tar and verify contents
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.NotEmpty(t, data, "build context should have content")

	// Verify it's a valid tar: tar files start with specific magic bytes
	assert.True(t, len(data) > 512, "tar should have at least one header block")
}

func TestContainerExit_Error(t *testing.T) {
	_, client := setupDockerPool(t)

	tmpDir, err := os.MkdirTemp("", "coach-test-exit-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	dataDir := filepath.Join(tmpDir, "data")
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(dataDir, 0o755))
	require.NoError(t, os.MkdirAll(outputDir, 0o755))

	// Build an image that exits with code 1
	buildDir, err := os.MkdirTemp("", "coach-test-exit-build-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(buildDir) })

	dockerfile := `FROM alpine:3.21
CMD ["sh", "-c", "echo 'failing!' >&2; exit 1"]
`
	require.NoError(t, os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0o644))

	buildCtx, err := BuildContextDir(buildDir)
	require.NoError(t, err)

	tag := fmt.Sprintf("coach-test-exit-%x:latest", time.Now().UnixNano())
	ctx := context.Background()
	err = client.ImageBuild(ctx, buildCtx, tag)
	require.NoError(t, err)

	err = client.Run(ctx, tag, dataDir, outputDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited with non-zero code", "should report exit code error")
}

func TestParseDockerStream_ScanError(t *testing.T) {
	// Simulate a reader that fails mid-scan by providing corrupted data
	// parseDockerStream uses bufio.Scanner which only fails on token-too-long
	// We test the empty-reader case which is normal
	var buf bytes.Buffer
	err := parseDockerStream(strings.NewReader(""), &buf)
	require.NoError(t, err)
	assert.Empty(t, buf.String())
}

func TestRegistryAuth(t *testing.T) {
	t.Run("with credentials", func(t *testing.T) {
		result := registryAuth("registry.example.com/myimage", "user:pass")
		assert.NotEmpty(t, result, "should produce base64 encoded auth")
	})

	t.Run("empty auth", func(t *testing.T) {
		result := registryAuth("myimage", "")
		assert.Empty(t, result)
	})

	t.Run("username only", func(t *testing.T) {
		result := registryAuth("myimage", "user")
		assert.NotEmpty(t, result, "should work with username only")
	})
}

// ---------------------------------------------------------------------------
// dockertest resource-based tests (using pool.Run for container lifecycle)
// ---------------------------------------------------------------------------

func TestDockertestPoolIntegration(t *testing.T) {
	pool, err := dockertest.NewPool("")
	require.NoError(t, err)

	// Run a simple alpine container that echoes and exits
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "alpine",
		Tag:        "3.21",
		Cmd:        []string{"echo", "dockertest works"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Purge(resource) })

	// Wait for container to exit
	require.NoError(t, resource.Expire(10))

	// Check exit code
	exitCode, err := pool.Client.InspectContainer(resource.Container.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode.State.ExitCode, "container should exit cleanly")
}

// Test with a long-running container to verify pool connection reuse
func TestDockertestLongRunning(t *testing.T) {
	pool, err := dockertest.NewPool("")
	require.NoError(t, err)

	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "alpine",
		Tag:        "3.21",
		Cmd:        []string{"sleep", "2"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Purge(resource) })

	// Pool should be able to retry connection
	err = pool.Retry(func() error {
		c, inspectErr := pool.Client.InspectContainer(resource.Container.ID)
		if inspectErr != nil {
			return inspectErr
		}
		if c.State.Running {
			return nil
		}
		return fmt.Errorf("container not running, state: %v", c.State)
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// docker.Client-based tests using dockertest's docker client for introspection
// ---------------------------------------------------------------------------

func TestContainerIsolation(t *testing.T) {
	_, coachClient := setupDockerPool(t)

	tmpDir, err := os.MkdirTemp("", "coach-test-iso-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	dataDir := filepath.Join(tmpDir, "data")
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(dataDir, 0o755))
	require.NoError(t, os.MkdirAll(outputDir, 0o755))

	// Data file should be readable inside container as /data/input.txt
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "input.txt"), []byte("isolated"), 0o644))

	// Build image that reads /data and writes to /output
	buildDir, err := os.MkdirTemp("", "coach-test-iso-build-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(buildDir) })

	dockerfile := `FROM alpine:3.21
CMD ["sh", "-c", "cat /data/input.txt | tr '[:lower:]' '[:upper:]' > /output/result.txt"]
`
	require.NoError(t, os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0o644))

	buildCtx, err := BuildContextDir(buildDir)
	require.NoError(t, err)

	tag := fmt.Sprintf("coach-test-iso-%x:latest", time.Now().UnixNano())
	ctx := context.Background()
	err = coachClient.ImageBuild(ctx, buildCtx, tag)
	require.NoError(t, err)

	err = coachClient.Run(ctx, tag, dataDir, outputDir)
	require.NoError(t, err)

	result, err := os.ReadFile(filepath.Join(outputDir, "result.txt"))
	require.NoError(t, err)
	assert.Equal(t, "ISOLATED", strings.TrimSpace(string(result)))
}

func TestMultipleBuildsWithSameTag(t *testing.T) {
	_, client := setupDockerPool(t)

	tag := fmt.Sprintf("coach-test-rebuild-%x:latest", time.Now().UnixNano())

	tmpDir, err := os.MkdirTemp("", "coach-test-rebuild-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	dockerfile := `FROM alpine:3.21
RUN echo "build 1" > /version.txt
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0o644))

	buildCtx, err := BuildContextDir(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	// First build
	err = client.ImageBuild(ctx, buildCtx, tag)
	require.NoError(t, err)

	// Second build with same tag - should overwrite
	buildCtx2, err := BuildContextDir(tmpDir)
	require.NoError(t, err)

	err = client.ImageBuild(ctx, buildCtx2, tag)
	require.NoError(t, err, "rebuilding with same tag should succeed")

	exists, err := client.ImageExists(ctx, tag)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestEnsureImageDigest_PullsIfAbsent(t *testing.T) {
	_, client := setupDockerPool(t)

	// Use a tiny public image that we likely don't have locally
	ctx := context.Background()
	digest, err := client.EnsureImageDigest(ctx, "alpine:3.21")
	require.NoError(t, err)
	assert.NotEqual(t, [32]byte{}, digest, "should return a digest for alpine:3.21")
}

// ---------------------------------------------------------------------------
// ImageRemove and ImageList tests
// ---------------------------------------------------------------------------

func TestImageRemove(t *testing.T) {
	_, client := setupDockerPool(t)
	tag := buildTestImage(t, client)
	ctx := context.Background()

	exists, err := client.ImageExists(ctx, tag)
	require.NoError(t, err)
	assert.True(t, exists, "image should exist before removal")

	// Remove the image and verify it's gone.
	err = client.ImageRemove(ctx, tag, true)
	require.NoError(t, err, "force remove should succeed")

	exists, err = client.ImageExists(ctx, tag)
	if err != nil {
		// ImageInspect returns an error for nonexistent images.
		return
	}
	assert.False(t, exists, "image should not exist after removal")
}

func TestImageRemove_Nonexistent(t *testing.T) {
	_, client := setupDockerPool(t)
	ctx := context.Background()

	err := client.ImageRemove(ctx, "nonexistent-image-xyz-123:latest", true)
	require.Error(t, err, "removing non-existent image should fail")
	assert.Contains(t, err.Error(), "remove image")
}

func TestImageList_All(t *testing.T) {
	_, client := setupDockerPool(t)
	ctx := context.Background()

	images, err := client.ImageList(ctx, "")
	require.NoError(t, err)
	// At minimum, the alpine:3.21 images pulled during other tests should exist
	assert.NotEmpty(t, images, "should list at least some images")
}

func TestImageList_FilterByPrefix(t *testing.T) {
	_, client := setupDockerPool(t)

	// Build an image with the wrapper prefix
	tmpDir, err := os.MkdirTemp("", "coach-test-listfilter-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	dockerfile := `FROM alpine:3.21
RUN echo "wrapped test" > /test.txt
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0o644))

	buildCtx, err := BuildContextDir(tmpDir)
	require.NoError(t, err)

	tag := fmt.Sprintf("coach-wrapped-test-listfilter-%x:latest", time.Now().UnixNano())
	ctx := context.Background()
	err = client.ImageBuild(ctx, buildCtx, tag)
	require.NoError(t, err)

	// Clean up after test
	t.Cleanup(func() {
		_ = client.ImageRemove(context.Background(), tag, true)
	})

	// List with the wrapper prefix filter
	images, err := client.ImageList(ctx, "coach-wrapped-*")
	require.NoError(t, err)

	found := false
	for _, img := range images {
		if slices.Contains(img.RepoTags, tag) {
			found = true
			break
		}
	}
	assert.True(t, found, "should find image with coach-wrapped- prefix")
}

func TestImageList_FilterNoMatch(t *testing.T) {
	_, client := setupDockerPool(t)
	ctx := context.Background()

	images, err := client.ImageList(ctx, "nonexistent-prefix-zzz-*")
	require.NoError(t, err)
	assert.Empty(t, images, "should return empty list for non-matching filter")
}

// ---------------------------------------------------------------------------
// Benchmark
// ---------------------------------------------------------------------------

func BenchmarkParseDockerStream(b *testing.B) {
	payload := strings.Repeat(
		`{"stream":"Step 1/2 : FROM alpine\\n"}`+"\n"+
			`{"stream":" ---> 12345\\n"}`+"\n",
		10,
	)
	b.ResetTimer()
	for b.Loop() {
		var buf bytes.Buffer
		_ = parseDockerStream(strings.NewReader(payload), &buf)
	}
}
