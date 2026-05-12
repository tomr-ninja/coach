package utils

import (
	"regexp"
	"strings"
)

var safeImageRegexp = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// SanitizeImageName converts a Docker image reference into a safe tag fragment.
func SanitizeImageName(name string) string {
	parts := strings.Split(name, "/")
	last := parts[len(parts)-1]
	last = strings.ReplaceAll(last, ":", "-")

	return safeImageRegexp.ReplaceAllString(last, "-")
}

// ShellQuote escapes a string for safe use in a POSIX shell.
func ShellQuote(s string) string {
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
