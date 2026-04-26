package artifacts

import "github.com/lukasstrickler/noto/internal/notoerr"

// Validation error codes for artifact validation.
const (
	// ErrCodeValidationFailed is the error code for validation failures.
	ErrCodeValidationFailed = "ERR_VALIDATION_FAILED"
	// ErrCodeChecksumMismatch is the error code for checksum mismatches.
	ErrCodeChecksumMismatch = "ERR_CHECKSUM_MISMATCH"
	// ErrCodeMissingField is the error code for missing required fields.
	ErrCodeMissingField = "ERR_MISSING_FIELD"
	// ErrCodeInvalidField is the error code for invalid field values.
	ErrCodeInvalidField = "ERR_INVALID_FIELD"
)

// NewValidationError creates a new validation error with the given field and message.
func NewValidationError(field, message string) *notoerr.Error {
	return notoerr.New(ErrCodeValidationFailed, message, map[string]any{"field": field})
}

// NewChecksumError creates a new checksum error.
func NewChecksumError(expected, actual string) *notoerr.Error {
	return notoerr.New(ErrCodeChecksumMismatch, "checksum mismatch", map[string]any{
		"expected": expected,
		"actual":   actual,
	})
}

// NewMissingFieldError creates a new missing field error.
func NewMissingFieldError(field string) *notoerr.Error {
	return notoerr.New(ErrCodeMissingField, field+" is required", map[string]any{"field": field})
}

// NewInvalidFieldError creates a new invalid field error.
func NewInvalidFieldError(field, message string) *notoerr.Error {
	return notoerr.New(ErrCodeInvalidField, message, map[string]any{"field": field})
}
