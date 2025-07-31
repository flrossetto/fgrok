// Package fgrokerr provides structured error handling for the application.
//
// Features:
// - Standardized error codes
// - Human-readable messages
// - Error chaining/tracing
// - Helper methods for error construction
//
// The Error type implements the standard error interface while providing
// additional context through error codes and nested errors.
package fgrokerr

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

//nolint:gochecknoglobals
var (
	// Is forwards to errors.Is for error comparison.
	Is = errors.Is

	// As forwards to errors.As for error type assertion.
	As = errors.As

	// Unwrap forwards to errors.Unwrap for error chain inspection.
	Unwrap = errors.Unwrap
)

// Details provides additional context for errors in key-value format.
// Used to attach debugging information and contextual metadata to errors.
type Details map[string]any

// ErrorOption defines optional parameters for error creation
type ErrorOption func(*Error)

// Code is a type that represents a machine-readable error identifier.
// It's used to standardize and categorize errors across the application.
type Code string

// WithError sets the underlying error
func WithError(err error) ErrorOption {
	return func(e *Error) {
		e.Err = err
	}
}

// WithDetails adds additional context details to the error
func WithDetails(details Details) ErrorOption {
	return func(e *Error) {
		if e.Details == nil {
			e.Details = make(Details)
		}

		for k, v := range details {
			e.Details[k] = v
		}
	}
}

// Error implements a structured error pattern for consistent error handling.
//
// Fields:
// - Code: Machine-readable error identifier (e.g., "NOT_FOUND")
// - Message: Human-readable error description
// - Err: Underlying error that caused this one (optional)
// - Details: Additional context information (optional)
//
// Example:
//
//	err := error.New("AUTH_FAIL", "authentication failed", error.WithError(dbErr))
type Error struct {
	Code    Code    // Machine-readable error code
	Message string  // Human-readable message
	Err     error   // Underlying error (optional)
	Details Details // Additional context details
}

// New creates a new structured error instance
//
// Parameters:
// - code: Machine-readable error code (use constants when possible)
// - message: Human-readable error description
// - opts: Optional parameters (WithError, WithDetails)
//
// Returns:
// - *Error: New error instance ready for use
//
// Example:
//
//	return error.New(
//	    error.CodeNotFound,
//	    "user not found",
//	    error.WithError(err),
//	    error.WithDetails(map[string]any{"user_id": 123}),
//	)
func New(code Code, message string, opts ...ErrorOption) error {
	err := &Error{
		Code:    code,
		Message: message,
	}
	for _, opt := range opts {
		opt(err)
	}

	return err
}

// Error implements the error interface for standard compatibility
//
// Returns:
// - string: Formatted error message including code, message and nested error
//
// Format:
//
//	"CODE: message (nested error)" when nested error exists
//	"CODE: message" when no nested error
func (e *Error) Error() string {
	var sb strings.Builder
	sb.WriteString(string(e.Code))
	sb.WriteString(": ")
	sb.WriteString(e.Message)

	if len(e.Details) > 0 {
		sb.WriteString(" [")

		first := true

		for k, v := range e.Details {
			if !first {
				sb.WriteString(", ")
			}

			fmt.Fprintf(&sb, "%s=%v", k, v)

			first = false
		}

		sb.WriteString("]")
	}

	if e.Err != nil {
		sb.WriteString(" (")
		sb.WriteString(e.Err.Error())
		sb.WriteString(")")
	}

	return sb.String()
}

// Unwrap returns the underlying error for error inspection
//
// Implements the standard Unwrap interface used by errors.Is/As
//
// Returns:
// - error: The original wrapped error (may be nil)
func (e *Error) Unwrap() error {
	return e.Err
}

// Is compares error codes for equality
//
// # Implements the standard Is interface used by errors.Is
//
// Parameters:
// - target: Error to compare against
//
// Returns:
// - bool: True if target is *Error with same Code
func (e *Error) Is(target error) bool {
	if t, ok := target.(*Error); ok {
		return e.Code == t.Code
	}

	return false
}

// ToGRPC converts a fgrok error to a gRPC status error
func ToGRPC(err error) error {
	if err == nil {
		return nil
	}

	var fgErr *Error
	if As(err, &fgErr) {
		switch fgErr.Code {
		case CodeInvalidInput:
			return status.Error(codes.InvalidArgument, fgErr.Error())
		case CodeNotFound:
			return status.Error(codes.NotFound, fgErr.Error())
		case CodeUnauthorized:
			return status.Error(codes.Unauthenticated, fgErr.Error())
		case CodeInternalError:
			return status.Error(codes.Internal, fgErr.Error())
		case CodeConnectionError:
			return status.Error(codes.Unavailable, fgErr.Error())
		case CodeConflict:
			return status.Error(codes.AlreadyExists, fgErr.Error())
		default:
			return status.Error(codes.Unknown, fgErr.Error())
		}
	}

	return status.Error(codes.Unknown, err.Error())
}

// Common error codes for consistent error handling
const (
	// CodeInvalidInput indicates invalid user input/parameters
	CodeInvalidInput Code = "INVALID_INPUT"

	// CodeNotFound indicates a requested resource was not found
	CodeNotFound Code = "NOT_FOUND"

	// CodeUnauthorized indicates authentication/authorization failure
	CodeUnauthorized Code = "UNAUTHORIZED"

	// CodeInternalError indicates an unexpected system error
	CodeInternalError Code = "INTERNAL_ERROR"

	// CodeNotImplemented indicates unfinished functionality
	CodeNotImplemented Code = "NOT_IMPLEMENTED"

	// CodeHostError indicates host-related validation failures
	CodeHostError Code = "HOST_ERROR"

	// CodeConfigError indicates configuration related errors
	CodeConfigError Code = "CONFIG_ERROR"

	// CodeConnectionError indicates connection failures
	CodeConnectionError Code = "CONNECTION_ERROR"

	// CodeTunnelError indicates tunnel related errors
	CodeTunnelError Code = "TUNNEL_ERROR"

	// CodeSerializationError indicates serialization/deserialization failures
	CodeSerializationError Code = "SERIALIZATION_ERROR"

	// CodeServerError indicates general server failures
	CodeServerError Code = "SERVER_ERROR"

	// CodeShutdownError indicates server shutdown failures
	CodeShutdownError Code = "SHUTDOWN_ERROR"

	// CodeConflict indicates a resource/operation conflict
	CodeConflict Code = "CONFLICT"
)
