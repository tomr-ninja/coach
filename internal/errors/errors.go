package errors

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
)

// Kind classifies errors by subsystem so callers can decide on recovery strategy.
type Kind int

const (
	KindUser     Kind = iota // misconfiguration, bad args, missing files
	KindIO                   // network, disk, Docker, S3
	KindDriver               // driver crash, protocol mismatch
	KindInternal             // invariants violated, should not happen
)

// Error is a typed error with optional cause, hint, and transient flag.
// It implements the standard error interface and supports Unwrap for errors.Is/As.
type Error struct {
	Kind      Kind
	Message   string
	Cause     error
	Suggest   string
	Transient bool
}

// New creates a new typed error without a cause.
func New(kind Kind, message string) *Error {
	return &Error{Kind: kind, Message: message}
}

// Wrap wraps an existing error with a kind and message.
func Wrap(cause error, kind Kind, message string) *Error {
	return &Error{
		Kind:    kind,
		Message: message,
		Cause:   cause,
	}
}

// WithHint attaches or replaces the hint on an error.
// If err is already an *Error, the hint is set on that error.
// Otherwise, a new Error wrapping err is created.
func WithHint(err error, hint string) *Error {
	if e, ok := errors.AsType[*Error](err); ok {
		e.Suggest = hint
		return e
	}
	return &Error{
		Kind:    KindInternal,
		Message: err.Error(),
		Cause:   err,
		Suggest: hint,
	}
}

// AsTransient marks an error as retryable.
// If err is already an *Error, Transient is set to true.
// Otherwise, a new Error wrapping err is created.
func AsTransient(err error) *Error {
	if e, ok := errors.AsType[*Error](err); ok {
		e.Transient = true
		return e
	}
	return &Error{
		Kind:      KindInternal,
		Message:   err.Error(),
		Cause:     err,
		Transient: true,
	}
}

// Error returns the user-facing message.
func (e *Error) Error() string {
	return e.Message
}

// Unwrap returns the wrapped cause for errors.Is/As compatibility.
func (e *Error) Unwrap() error {
	return e.Cause
}

// Format returns user-facing output: the message followed by a hint if present.
func (e *Error) Format() string {
	if e.Suggest == "" {
		return e.Message
	}
	return fmt.Sprintf("%s\nHint: %s", e.Message, e.Suggest)
}

// DebugFormat returns a full error chain for debugging output.
// Each link in the chain is printed on its own indented line.
func DebugFormat(err error) string {
	var b strings.Builder
	current := err
	indent := ""
	for current != nil {
		b.WriteString(indent)
		b.WriteString(current.Error())
		if ce, ok := errors.AsType[*Error](current); ok {
			if ce.Suggest != "" {
				b.WriteString(" [Hint: ")
				b.WriteString(ce.Suggest)
				b.WriteByte(']')
			}
			if ce.Transient {
				b.WriteString(" [transient]")
			}
			current = ce.Cause
		}
		if current != nil {
			b.WriteByte('\n')
			indent += "  "
		}
	}
	return b.String()
}

// GetKind extracts the Kind from an error chain. Returns KindInternal for unrecognised errors.
func GetKind(err error) Kind {
	if err == nil {
		return KindInternal
	}
	for {
		if e, ok := errors.AsType[*Error](err); ok {
			return e.Kind
		}
		// Continue walking the chain via Unwrap.
		// Standard errors.Join has no Unwrap; %w chains are handled by the Error check above.
		unwrapped := unwrap(err)
		if unwrapped == nil {
			break
		}
		err = unwrapped
	}
	return KindInternal
}

// unwrap is a helper that calls Unwrap() if the error supports it.
// Avoids importing "errors" from stdlib to prevent name collision;
// we implement our own minimal chain walker.
type unwrapper interface {
	Unwrap() error
}

func unwrap(err error) error {
	u, ok := err.(unwrapper)
	if !ok {
		return nil
	}
	return u.Unwrap()
}

// GetHint extracts the first hint from an error chain. Returns "" if none found.
func GetHint(err error) string {
	for {
		if e, ok := errors.AsType[*Error](err); ok && e.Suggest != "" {
			return e.Suggest
		}
		u := unwrap(err)
		if u == nil {
			return ""
		}
		err = u
	}
}

// IsTransient reports whether any error in the chain is marked as transient.
func IsTransient(err error) bool {
	for {
		if e, ok := errors.AsType[*Error](err); ok && e.Transient {
			return true
		}
		// Treat network-level errors as transient.
		if isNetworkErrorLeaf(err) {
			return true
		}
		u := unwrap(err)
		if u == nil {
			return false
		}
		err = u
	}
}

// IsNetworkError reports whether err is a network-level error (DNS, timeout, connection refused)
// as opposed to an application-level error (HTTP 4xx, auth failure, etc.).
//
// Covers Go's standard net.OpError, net.DNSError, syscall errors, and os.IsTimeout.
func IsNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// Walk the error chain.
	for {
		if isNetworkErrorLeaf(err) {
			return true
		}
		u := unwrap(err)
		if u == nil {
			return false
		}
		err = u
	}
}

func isNetworkErrorLeaf(err error) bool {
	// net.OpError covers most I/O failures: dial, read, write timeouts, connection refused.
	if _, ok := errors.AsType[*net.OpError](err); ok {
		return true
	}

	// net.DNSError covers DNS failures (NXDOMAIN, no such host, timeout).
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return true
	}

	// syscall errors: ECONNREFUSED, ECONNRESET, ETIMEDOUT, EHOSTUNREACH, ENETUNREACH.
	if isTransientSyscall(err) {
		return true
	}

	// os.ErrDeadlineExceeded or any error satisfying os.IsTimeout.
	if os.IsTimeout(err) {
		return true
	}

	return false
}

func isTransientSyscall(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch {
	case errors.Is(errno, syscall.ECONNREFUSED):
		return true
	case errors.Is(errno, syscall.ECONNRESET):
		return true
	case errors.Is(errno, syscall.ETIMEDOUT):
		return true
	case errors.Is(errno, syscall.EHOSTUNREACH):
		return true
	case errors.Is(errno, syscall.ENETUNREACH):
		return true
	case errors.Is(errno, syscall.EPIPE):
		return true
	default:
		return false
	}
}

func ExitCode(kind Kind) int {
	switch kind {
	case KindUser:
		return 2
	case KindIO:
		return 3
	case KindDriver:
		return 4
	default:
		return 1
	}
}
