package coach

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tomr-ninja/coach/docker"
	"github.com/tomr-ninja/coach/internal/utils"
)

const wrapperImagePrefix = "coach-wrapped-"

// CleanupResult holds the outcome of a cleanup operation.
type CleanupResult struct {
	Removed int
	Errors  int
}

// Cleanup removes stale coach wrapper images older than maxAge.
// If maxAge is 0, all wrapper images are removed regardless of age.
// If dryRun is true, images are only printed, not removed.
func Cleanup(ctx context.Context, maxAge time.Duration, dryRun bool) (*CleanupResult, error) {
	client, err := docker.NewClient()
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	if utils.IsVerbose(ctx) {
		fmt.Fprintf(os.Stderr, "listing wrapper images...\n")
	}
	images, err := client.ImageList(ctx, wrapperImagePrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("list wrapper images: %w", err)
	}

	var result CleanupResult

	for _, img := range images {
		// Only match images that have at least one tag with the wrapper prefix.
		hasWrapperTag := false
		var tag string
		for _, t := range img.RepoTags {
			if len(t) > len(wrapperImagePrefix) && t[:len(wrapperImagePrefix)] == wrapperImagePrefix {
				hasWrapperTag = true
				tag = t
				break
			}
		}
		if !hasWrapperTag {
			continue
		}

		if maxAge > 0 {
			created := time.Unix(img.Created, 0)
			if time.Since(created) < maxAge {
				continue
			}
		}

		if dryRun {
			fmt.Printf("would remove: %s (created %s)\n", tag, time.Unix(img.Created, 0).Format(time.RFC3339))
			result.Removed++
			continue
		}

		if err := client.ImageRemove(ctx, img.ID, true); err != nil {
			fmt.Fprintf(os.Stderr, "warning: remove image %s: %v\n", tag, err)
			result.Errors++
			continue
		}

		fmt.Printf("removed: %s\n", tag)
		result.Removed++
	}

	return &result, nil
}
