package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cmdLogTee reads stdin line by line, writes each line to stdout, and also
// appends raw text to <output-dir>/.coach/log.txt.
// The log file is fsync'd at least every --flush-interval seconds (default 5s).
//
// Usage: coach-sidecar log-tee --output-dir <dir> [--flush-interval <seconds>]
func cmdLogTee(args []string) {
	var outputDir string
	flushInterval := 5 * time.Second

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output-dir":
			i++
			if i < len(args) {
				outputDir = args[i]
			}
		case "--flush-interval":
			i++
			if i < len(args) {
				d, err := parseSecondsOrDuration(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "log-tee: invalid --flush-interval %q: %v\n", args[i], err)
					os.Exit(1)
				}
				flushInterval = d
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown log-tee argument: %s\n", args[i])
			os.Exit(1)
		}
	}

	if outputDir == "" {
		fmt.Fprintf(os.Stderr, "usage: coach-sidecar log-tee --output-dir <dir> [--flush-interval <seconds>]\n")
		os.Exit(1)
	}

	if err := doLogTee(outputDir, flushInterval); err != nil {
		fmt.Fprintf(os.Stderr, "log-tee: %v\n", err)
		os.Exit(1)
	}
}

func doLogTee(outputDir string, flushInterval time.Duration) error {
	logDir := filepath.Join(outputDir, ".coach")
	// #nosec G703 -- outputDir is trusted (sidecar CLI, configured by entrypoint)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	logPath := filepath.Join(logDir, "log.txt")
	// #nosec G703 -- logPath derived from trusted outputDir
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	// Background goroutine periodically fsyncs the log file for durability.
	stop := make(chan struct{})
	flusherDone := make(chan struct{})
	go func() {
		defer close(flusherDone)
		ticker := time.NewTicker(flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = f.Sync()
			}
		}
	}()

	stopFlusher := func() {
		close(stop)
		<-flusherDone
	}

	// Use bufio.Reader (no fixed buffer) instead of bufio.Scanner
	// so that arbitrarily long lines don't kill the pipeline.
	reader := bufio.NewReader(os.Stdin)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			if readErr != io.EOF {
				stopFlusher()
				return fmt.Errorf("read stdin: %w", readErr)
			}
			// EOF with partial line: flush it.
			if line == "" {
				break
			}
		} else {
			line = strings.TrimSuffix(line, "\n")
		}

		// Write to stdout so Docker/vendor dashboard sees it.
		if _, err := fmt.Println(line); err != nil {
			stopFlusher()
			return fmt.Errorf("write stdout: %w", err)
		}

		// Write directly to the log file (unbuffered) so every line
		// is immediately visible to readers like the Phase 3 upload.
		if _, err := fmt.Fprintln(f, line); err != nil {
			stopFlusher()
			return fmt.Errorf("write log file: %w", err)
		}

		if readErr == io.EOF {
			break
		}
	}

	close(stop)
	<-flusherDone

	return f.Sync()
}

// parseSecondsOrDuration parses a bare number as seconds, or a Go duration string.
func parseSecondsOrDuration(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	return time.ParseDuration(s)
}
