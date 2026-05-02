package main

import (
	"regexp"
	"strconv"
	"strings"
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

func parseCPU(s string) uint32 {
	if s == "" {
		return 560
	}

	if strings.HasSuffix(s, "m") {
		v, err := strconv.ParseUint(s[:len(s)-1], 10, 32)
		if err == nil {
			return uint32(v)
		}
		return 560
	}

	f, err := strconv.ParseFloat(s, 64)
	if err == nil {
		return uint32(f * 1000)
	}

	return 560
}

func parseMemory(s string) uint32 {
	if s == "" {
		return 1024
	}

	multiplier := map[string]uint32{
		"Ki": 0,
		"Mi": 1,
		"Gi": 1024,
		"Ti": 1024 * 1024,
		"Pi": 1024 * 1024 * 1024,
		"K":  0,
		"M":  0,
		"G":  1024,
		"T":  1024 * 1024,
		"P":  1024 * 1024 * 1024,
	}

	for suffix, mult := range multiplier {
		if !strings.HasSuffix(s, suffix) {
			continue
		}
		v, err := strconv.ParseFloat(s[:len(s)-len(suffix)], 64)
		if err != nil {
			return 1024
		}

		if strings.HasPrefix(suffix, "K") {
			v /= 1024
		} else if mult > 0 {
			v *= float64(mult)
		}

		if v < 1 {
			v = 1
		}
		return uint32(v)
	}

	v, err := strconv.ParseFloat(s, 64)
	if err == nil {
		if v < 1 {
			v = 1
		}
		return uint32(v)
	}

	return 1024
}
