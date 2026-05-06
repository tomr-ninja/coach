package protocol

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseCPU(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  uint32
	}{
		{"empty", "", 0},
		{"millicores exact", "500m", 500},
		{"millicores large", "2000m", 2000},
		{"whole number", "2", 2000},
		{"float", "1.5", 1500},
		{"float with fraction", "0.25", 250},
		{"invalid", "abc", 0},
		{"invalid suffix", "100x", 0},
		{"negative millicores", "-100m", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseCPU(tt.input), "input: %q", tt.input)
		})
	}
}

func TestParseMemory(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  uint32
	}{
		{"empty", "", 0},
		{"bytes bare number", "512", 512},
		{"bytes rounds up to 1", "0", 1},
		{"bytes fraction rounds up", "0.5", 1},
		{"Ki", "1024Ki", 1},
		{"Ki fractional", "512Ki", 1},
		{"Mi exact", "256Mi", 256},
		{"Gi exact", "4Gi", 4096},
		{"Gi fractional", "1.5Gi", 1536},
		{"Ti exact", "2Ti", 2097152},
		{"Pi exact", "1Pi", 1073741824},
		{"K shorthand", "1024K", 1},
		{"K shorthand fractional", "512K", 1},
		{"M shorthand", "512M", 512},
		{"G shorthand", "2G", 2048},
		{"T shorthand", "1T", 1048576},
		{"P shorthand", "1P", 1073741824},
		{"invalid", "abc", 0},
		{"invalid suffix", "100X", 0},
		{"negative bare rounds up", "-100", 1},
		{"Mi negative rounds up", "-128Mi", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseMemory(tt.input), "input: %q", tt.input)
		})
	}
}

func TestParseGPU(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  uint32
	}{
		{"empty", "", 0},
		{"zero", "0", 0},
		{"one", "1", 1},
		{"eight", "8", 8},
		{"invalid", "abc", 0},
		{"float invalid", "1.5", 0},
		{"negative", "-1", 0},
		{"large", "2147483647", 2147483647},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseGPU(tt.input), "input: %q", tt.input)
		})
	}
}

func TestParseResources(t *testing.T) {
	got := ParseResources("500m", "256Mi", "2", "H100")
	want := Resources{
		CPUMillicores: 500,
		MemoryMi:      256,
		GPU:           2,
		GPUType:       "H100",
	}
	assert.Equal(t, want, got)

	got2 := ParseResources("", "", "", "")
	want2 := Resources{}
	assert.Equal(t, want2, got2)
}
