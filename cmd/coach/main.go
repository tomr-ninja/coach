package main

import (
	"bufio"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tomr-ninja/coach/docker"
)

func main() {
	if len(os.Args) < 2 {
		fatal("usage: coach <command> [<args>]\ncommands: run")
	}

	switch os.Args[1] {
	case "run":
		var (
			model  string
			data   string
			output string
		)

		runFlags := flag.NewFlagSet("run", flag.ExitOnError)
		runFlags.StringVar(&model, "model", "", "Docker image of the model")
		runFlags.StringVar(&data, "data", "", "Path to the data folder")
		runFlags.StringVar(&output, "output", "", "Path to the output folder")
		if err := runFlags.Parse(os.Args[2:]); err != nil {
			fatal("error parsing flags: %v", err)
		}

		if model == "" || data == "" || output == "" {
			fatal("flags -model, -data, and -output are required")
		}

		run(model, data, output)
	default:
		fatal("unknown command: %s", os.Args[1])
	}
}

func run(modelImage, dataDir, outputDir string) {
	client, err := docker.NewRealDockerClient()
	if err != nil {
		fatal("create docker client: %v", err)
	}
	defer client.Close()

	digest, err := client.ImageDigest(modelImage)
	if err != nil {
		fatal("image digest: %v", err)
	}

	chunkChecksums, err := collectDataChecksums(dataDir)
	if err != nil {
		fatal("collect data checksums: %v", err)
	}

	fingerprint := computeFingerprint(digest, chunkChecksums)
	artifactsDir := filepath.Join(outputDir, fmt.Sprintf("%x", fingerprint))

	if err := client.Run(modelImage, dataDir, artifactsDir); err != nil {
		fatal("run: %v", err)
	}

	if _, err := os.Stat(artifactsDir); err != nil {
		fatal("model exited successfully but output %s does not exist", artifactsDir)
	}

	fmt.Printf("%x\n", fingerprint)
}

func collectDataChecksums(dataDir string) ([][32]byte, error) {
	includeFile := filepath.Join(dataDir, ".coachinclude")
	ignoreFile := filepath.Join(dataDir, ".coachignore")

	useWhitelist := false
	var whitelist map[string]bool
	if _, err := os.Stat(includeFile); err == nil {
		useWhitelist = true
		whitelist = make(map[string]bool)
		f, err := os.Open(includeFile)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				whitelist[line] = true
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}

	var blacklist map[string]bool
	if !useWhitelist {
		if _, err := os.Stat(ignoreFile); err == nil {
			blacklist = make(map[string]bool)
			f, err := os.Open(ignoreFile)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line != "" {
					blacklist[line] = true
				}
			}
			if err := scanner.Err(); err != nil {
				return nil, err
			}
		}
	}

	var checksums [][32]byte
	err := filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}

		if rel == ".coachinclude" || rel == ".coachignore" {
			return nil
		}

		if useWhitelist {
			if !whitelist[rel] {
				return nil
			}
		} else if blacklist[rel] {
			return nil
		}

		h, err := fileSHA256(path)
		if err != nil {
			return err
		}
		checksums = append(checksums, h)
		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.SortFunc(checksums, func(a, b [32]byte) int {
		for i := range a {
			if a[i] < b[i] {
				return -1
			}
			if a[i] > b[i] {
				return 1
			}
		}
		return 0
	})

	return checksums, nil
}

func fileSHA256(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

func computeFingerprint(modelDigest [32]byte, chunkChecksums [][32]byte) [32]byte {
	sorted := slices.Clone(chunkChecksums)
	slices.SortFunc(sorted, func(a, b [32]byte) int {
		for i := range a {
			if a[i] < b[i] {
				return -1
			}
			if a[i] > b[i] {
				return 1
			}
		}
		return 0
	})

	h := sha256.New()
	h.Write(modelDigest[:])
	for _, ch := range sorted {
		h.Write(ch[:])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func fatal(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
