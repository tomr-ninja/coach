package validate

import (
	"strconv"
	"strings"

	"github.com/tomr-ninja/coach/protocol"
)

// ParseCPU converts a user-facing CPU string to millicores.
// Accepts bare numbers (treated as cores, e.g. "1.5" = 1500m)
// or explicit millicore values (e.g. "500m").
// Returns 0 for unparseable input.
func ParseCPU(s string) uint32 {
	if s == "" {
		return 0
	}

	if strings.HasSuffix(s, "m") {
		v, err := strconv.ParseUint(s[:len(s)-1], 10, 32)
		if err == nil {
			return uint32(v)
		}
		return 0
	}

	f, err := strconv.ParseFloat(s, 64)
	if err == nil {
		return uint32(f * 1000)
	}

	return 0
}

// ParseMemory converts a user-facing memory string to Mi (mebibytes).
// Recognises suffixes: Ki, Mi, Gi, Ti, Pi and their shorthand K, M, G, T, P.
// Bare numbers are treated as Mi. Values below 1 are rounded up to 1.
// Returns 0 for unparseable input.
func ParseMemory(s string) uint32 {
	if s == "" {
		return 0
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
			return 0
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

	return 0
}

// ParseGPU converts a user-facing GPU count string to a uint32.
// Only bare integer strings are accepted (e.g. "2"). Floats and negatives
// are rejected (return 0).
// Returns 0 for empty or unparseable input.
func ParseGPU(s string) uint32 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(v)
}

// ParseResources combines the four resource flags into a protocol.Resources value.
// Empty strings are treated as "not specified" and produce zero values.
func ParseResources(cpu, memory, gpu, gpuType string) protocol.Resources {
	return protocol.Resources{
		CPUMillicores: ParseCPU(cpu),
		MemoryMi:      ParseMemory(memory),
		GPU:           ParseGPU(gpu),
		GPUType:       gpuType,
	}
}
