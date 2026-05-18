package notoapi

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Error is the wire shape for an API failure. Mirrors internal/notoerr.Error
// but is JSON-only so we can pass it across the boundary.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewError(code, message string, details map[string]any) *Error {
	return &Error{Code: code, Message: message, Details: details}
}

// As converts a generic error to an *Error if possible.
func As(err error) (*Error, bool) {
	if err == nil {
		return nil, false
	}
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// ErrorEnvelope is the JSON body of any non-2xx HTTP response.
type ErrorEnvelope struct {
	Error *Error `json:"error"`
}

func (e *ErrorEnvelope) Err() error {
	if e == nil || e.Error == nil {
		return nil
	}
	return e.Error
}

// Common error codes mirroring docs/architecture.md.
const (
	CodeNotFound              = "not_found"
	CodeInvalidRequest        = "invalid_request"
	CodeConflict              = "conflict"
	CodePermissionDenied      = "permission_denied"
	CodeRecordingActive       = "recording_active"
	CodeRecordingInactive     = "recording_inactive"
	CodeJobNotFound           = "job_not_found"
	CodeProviderFailed        = "provider_failed"
	CodeSchemaValidationFailed = "schema_validation_failed"
	CodeArtifactConflict      = "artifact_conflict"
	CodeUnsupportedCapability = "unsupported_capability"
	CodeRetryableRemoteError  = "retryable_remote_error"
	CodeInternal              = "internal"
)

// Errorf is a convenience constructor.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// MarshalErrorJSON wraps an *Error in an envelope for HTTP responses.
func MarshalErrorJSON(e *Error) []byte {
	b, _ := json.Marshal(ErrorEnvelope{Error: e})
	return b
}
