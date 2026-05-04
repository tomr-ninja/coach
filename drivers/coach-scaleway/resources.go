package main

import (
	"regexp"
	"strings"
)

const (
	defaultCPU = 560
	defaultMem = 1024
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

	if len(name) < 3 {
		name += "-job"
	}

	return strings.ToLower(name)
}

func defaultIfZero(v, def uint32) uint32 {
	if v == 0 {
		return def
	}
	return v
}
