// Package version provides build-time version information.
// The Commit variable is set via ldflags at build time:
//
//	go build -ldflags "-X github.com/tomr-ninja/coach/internal/version.Commit=$(git rev-parse HEAD)" ./cmd/coach
package version

// Commit is the git commit hash of the build. It is set via ldflags at build
// time. When empty ("unknown"), the build was done without ldflags, and the
// Dockerfile template will default to the main branch.
var Commit = "unknown"
