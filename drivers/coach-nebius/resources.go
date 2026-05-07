package main

import (
	"regexp"
	"strings"
)

const (
	defaultPlatform = "gpu-l40s-a"
	defaultPreset   = "1gpu-8vcpu-32gb"
	defaultDiskGB   = 250
	defaultTimeout  = "24h"
)

var nonNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func sanitizeName(s string) string {
	name := nonNameChars.ReplaceAllString(s, "-")
	name = strings.ReplaceAll(name, "__", "-")
	name = strings.ReplaceAll(name, "--", "-")

	if len(name) > 50 {
		name = name[:50]
	}
	name = strings.Trim(name, "-")

	if len(name) == 0 {
		return "coach-job"
	}

	if len(name) < 3 {
		name += "-job"
	}

	return strings.ToLower(name)
}
