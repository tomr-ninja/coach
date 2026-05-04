package protocol

import (
	"strconv"
	"strings"
)

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

func ParseResources(cpu, memory, gpu, gpuType string) Resources {
	return Resources{
		CPUMillicores: ParseCPU(cpu),
		MemoryMi:      ParseMemory(memory),
		GPU:           ParseGPU(gpu),
		GPUType:       gpuType,
	}
}
