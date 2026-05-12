package coach

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tomr-ninja/coach/docker"
)

// setupCleanupTest builds a Docker image tagged with the coach-wrapped- prefix
// and returns the tag. The caller is responsible for cleanup.
func setupCleanupTest(t *testing.T) (tag string, cleanup func()) {
	t.Helper()

	client, err := docker.NewClient()
	require.NoError(t, err, "Docker daemon must be running for cleanup tests")
	t.Cleanup(func() { client.Close() })

	tmpDir, err := os.MkdirTemp("", "coach-cleanup-test-*")
	require.NoError(t, err)

	// Include a unique marker to prevent Docker layer deduplication
	// when multiple images are built in the same test.
	uniqueMarker := fmt.Sprintf("%x", time.Now().UnixNano())
	dockerfile := fmt.Sprintf(`FROM alpine:3.21
RUN echo "cleanup test %s" > /test.txt
`, uniqueMarker)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0o644))

	buildCtx, err := docker.BuildContextDir(tmpDir)
	require.NoError(t, err)

	tag = fmt.Sprintf("coach-wrapped-test-cleanup-%x:latest", time.Now().UnixNano())
	ctx := context.Background()
	err = client.ImageBuild(ctx, buildCtx, tag, "")
	require.NoError(t, err, "build should succeed")

	cleanup = func() {
		os.RemoveAll(tmpDir)
		// Best-effort cleanup in case the test didn't remove it
		_ = client.ImageRemove(context.Background(), tag, true)
	}

	return tag, cleanup
}

func imageExists(t *testing.T, client *docker.Client, tag string) bool {
	t.Helper()
	exists, err := client.ImageExists(context.Background(), tag)
	if err != nil {
		return false
	}
	return exists
}

func TestCleanup_RemovesAllWrapperImages(t *testing.T) {
	tag1, cleanup1 := setupCleanupTest(t)
	defer cleanup1()
	tag2, cleanup2 := setupCleanupTest(t)
	defer cleanup2()

	client, err := docker.NewClient()
	require.NoError(t, err)
	defer client.Close()

	// Both images should exist before cleanup
	assert.True(t, imageExists(t, client, tag1), "tag1 should exist before cleanup")
	assert.True(t, imageExists(t, client, tag2), "tag2 should exist before cleanup")

	// Run cleanup with maxAge=0 (remove all)
	ctx := context.Background()
	result, err := Cleanup(ctx, 0, false)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, result.Removed, 2, "should remove at least the 2 wrapper images we built")
	assert.Equal(t, 0, result.Errors)

	// Both images should be gone
	assert.False(t, imageExists(t, client, tag1), "tag1 should be removed")
	assert.False(t, imageExists(t, client, tag2), "tag2 should be removed")
}

func TestCleanup_DryRun(t *testing.T) {
	tag, cleanup := setupCleanupTest(t)
	defer cleanup()

	client, err := docker.NewClient()
	require.NoError(t, err)
	defer client.Close()

	assert.True(t, imageExists(t, client, tag), "image should exist before dry-run cleanup")

	ctx := context.Background()
	result, err := Cleanup(ctx, 0, true)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, result.Removed, 1, "dry-run should count at least 1 image")
	assert.Equal(t, 0, result.Errors)

	// Image should still exist after dry-run
	assert.True(t, imageExists(t, client, tag), "image should still exist after dry-run")
}

func TestCleanup_RespectsMaxAge(t *testing.T) {
	tag, cleanup := setupCleanupTest(t)
	defer cleanup()

	client, err := docker.NewClient()
	require.NoError(t, err)
	defer client.Close()

	assert.True(t, imageExists(t, client, tag), "image should exist before cleanup")

	// Set maxAge to 100 years — all images are newer, so none should be removed
	ctx := context.Background()
	result, err := Cleanup(ctx, 100*365*24*time.Hour, false)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Removed, "no images should be removed with very large maxAge")
	assert.Equal(t, 0, result.Errors)

	// Image should still exist
	assert.True(t, imageExists(t, client, tag), "image should still exist")
}

func TestCleanup_NoWrapperImages(t *testing.T) {
	// First, remove any existing wrapper images (best effort)
	ctx := context.Background()
	_, err := Cleanup(ctx, 0, false)
	require.NoError(t, err)

	// Now run cleanup when there should be no wrapper images
	result, err := Cleanup(ctx, 0, false)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Removed, "should remove 0 images when none exist")
	assert.Equal(t, 0, result.Errors)
}
