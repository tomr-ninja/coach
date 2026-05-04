package coach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/tomr-ninja/coach/protocol"
)

const (
	defaultDriverTimeout = 5 * time.Minute
	maxDriverOutput      = 10 * 1024 * 1024
)

var (
	errDriverFailure        = errors.New("driver returned failure")
	errDriverEmptyError     = errors.New("driver returned failure without error message")
	errDriverNotFile        = errors.New("driver is a directory, not an executable")
	errDriverNotExecutable  = errors.New("driver is not executable")
	errDriverOutputTooLarge = errors.New("driver output exceeded limit")
)

func ValidateDriver(driverPath string) error {
	stat, err := os.Stat(driverPath)
	if err != nil {
		return fmt.Errorf("validate driver %s: %w", driverPath, err)
	}
	if stat.IsDir() {
		return fmt.Errorf("validate driver %s: %w", driverPath, errDriverNotFile)
	}
	if stat.Mode()&0o111 == 0 {
		return fmt.Errorf("validate driver %s: %w", driverPath, errDriverNotExecutable)
	}
	return nil
}

func InvokeDriver(driverPath string, spec *protocol.JobSpec, backendConfig json.RawMessage) (*protocol.DriverResult, error) {
	return InvokeDriverWithContext(context.Background(), driverPath, spec, backendConfig)
}

func InvokeDriverWithContext(ctx context.Context, driverPath string, spec *protocol.JobSpec, backendConfig json.RawMessage) (*protocol.DriverResult, error) {
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, defaultDriverTimeout)
	defer cancel()

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal job spec: %w", err)
	}

	cmd := exec.CommandContext(ctx, driverPath)
	cmd.Stdin = bytes.NewReader(specJSON)
	cmd.Env = append(os.Environ(), "COACH_BACKEND_CONFIG="+string(backendConfig))

	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("driver %s timed out after %s: %w", driverPath, defaultDriverTimeout, err)
		}
		return nil, fmt.Errorf("driver %s failed: %w\nstderr: %s", driverPath, err, stderr.String())
	}

	var result protocol.DriverResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parse driver output: %w\nstdout: %s", err, stdout.String())
	}

	if !result.Success {
		if result.Error != "" {
			return nil, fmt.Errorf("%w: %s", errDriverFailure, result.Error)
		}
		return nil, errDriverEmptyError
	}

	return &result, nil
}

type limitedBuffer struct {
	buf bytes.Buffer
}

func (lb *limitedBuffer) Write(p []byte) (int, error) {
	if lb.buf.Len()+len(p) > maxDriverOutput {
		return 0, fmt.Errorf("%w: %d bytes", errDriverOutputTooLarge, maxDriverOutput)
	}
	return lb.buf.Write(p)
}

func (lb *limitedBuffer) String() string {
	return lb.buf.String()
}

func (lb *limitedBuffer) Bytes() []byte {
	return lb.buf.Bytes()
}
