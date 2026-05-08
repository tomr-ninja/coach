package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	coacherrors "github.com/tomr-ninja/coach/internal/errors"
	"github.com/tomr-ninja/coach/protocol"
)

const (
	maxDriverOutput      = 10 * 1024 * 1024
	defaultDriverTimeout = 5 * time.Minute
)

var (
	errDriverFailure        = coacherrors.New(coacherrors.KindDriver, "driver returned failure")
	errDriverEmptyError     = errors.New("driver returned failure without error message")
	errDriverNotFile        = coacherrors.New(coacherrors.KindUser, "driver is a directory, not an executable")
	errDriverNotExecutable  = coacherrors.New(coacherrors.KindUser, "driver is not executable")
	errDriverOutputTooLarge = coacherrors.New(coacherrors.KindInternal, "driver output exceeded limit")
	errDriverVersion        = coacherrors.New(coacherrors.KindDriver, "driver protocol version mismatch")
)

// Validate checks that the driver path points to an executable file.
func Validate(driverPath string) error {
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

// Invoke runs a driver with the default background context and timeout.
func Invoke(driverPath string, spec *protocol.Spec, backendConfig json.RawMessage) (*protocol.DriverResult, error) {
	return InvokeWithContext(context.Background(), driverPath, spec, backendConfig, defaultDriverTimeout)
}

// InvokeWithContext runs a driver with a given context and timeout.
func InvokeWithContext(ctx context.Context, driverPath string, spec *protocol.Spec, backendConfig json.RawMessage, timeout time.Duration) (*protocol.DriverResult, error) {
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}

	cmd := exec.CommandContext(ctx, driverPath)
	cmd.Stdin = io.LimitReader(bytes.NewReader(specJSON), maxDriverOutput)
	cmd.Env = append(os.Environ(), "COACH_BACKEND_CONFIG="+string(backendConfig))
	if verbose := ctx.Value("coach-verbose"); verbose != nil {
		cmd.Env = append(cmd.Env, "COACH_VERBOSE=1")
	}

	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Exit code non-zero = driver crashed/bugged. Exit code 0 with success:false = operational error.
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("driver %s timed out after %s: %w", driverPath, timeout, err)
		}
		return nil, fmt.Errorf("driver %s crashed: %w\nstderr: %s", driverPath, err, stderr.String())
	}

	var result protocol.DriverResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parse driver output: %w\nstdout: %s", err, stdout.String())
	}

	if result.ProtocolVersion != protocol.Version {
		return nil, fmt.Errorf("%w: driver v%d, coach v%d", errDriverVersion, result.ProtocolVersion, protocol.Version)
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
