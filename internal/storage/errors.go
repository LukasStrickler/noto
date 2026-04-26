package storage

import "github.com/lukasstrickler/noto/internal/notoerr"

// Storage error codes.
const (
	// ErrCodeStorageNotFound is returned when a meeting or artifact is not found.
	ErrCodeStorageNotFound = "ERR_STORAGE_NOT_FOUND"
	// ErrCodeStorageWriteFailed is returned when a write operation fails.
	ErrCodeStorageWriteFailed = "ERR_STORAGE_WRITE_FAILED"
	// ErrCodeStorageReadFailed is returned when a read operation fails.
	ErrCodeStorageReadFailed = "ERR_STORAGE_READ_FAILED"
	// ErrCodeChecksumMismatch is returned when a checksum verification fails.
	ErrCodeChecksumMismatch = "ERR_CHECKSUM_MISMATCH"
	// ErrCodeDirCreateFailed is returned when directory creation fails.
	ErrCodeDirCreateFailed = "ERR_DIR_CREATE_FAILED"
	// ErrCodeInvalidLayout is returned when the layout path is invalid.
	ErrCodeInvalidLayout = "ERR_INVALID_LAYOUT"
	// ErrCodeAtomicWriteFailed is returned when an atomic write operation fails.
	ErrCodeAtomicWriteFailed = "ERR_ATOMIC_WRITE_FAILED"
)

// ErrMeetingNotFound creates an error for a missing meeting.
func ErrMeetingNotFound(meetingID string) *notoerr.Error {
	return notoerr.New(ErrCodeStorageNotFound, "meeting not found", map[string]any{
		"meeting_id": meetingID,
	})
}

// ErrArtifactNotFound creates an error for a missing artifact.
func ErrArtifactNotFound(artifactType, meetingID string) *notoerr.Error {
	return notoerr.New(ErrCodeStorageNotFound, "artifact not found", map[string]any{
		"artifact_type": artifactType,
		"meeting_id":    meetingID,
	})
}

// ErrWriteFailed creates an error for a failed write operation.
func ErrWriteFailed(path string, err error) *notoerr.Error {
	details := map[string]any{"path": path}
	if err != nil {
		details["cause"] = err.Error()
	}
	return notoerr.New(ErrCodeStorageWriteFailed, "failed to write artifact", details)
}

// ErrReadFailed creates an error for a failed read operation.
func ErrReadFailed(path string, err error) *notoerr.Error {
	details := map[string]any{"path": path}
	if err != nil {
		details["cause"] = err.Error()
	}
	return notoerr.New(ErrCodeStorageReadFailed, "failed to read artifact", details)
}

// ErrChecksumMismatch creates an error for a checksum mismatch.
func ErrChecksumMismatch(expected, actual, path string) *notoerr.Error {
	return notoerr.New(ErrCodeChecksumMismatch, "checksum mismatch", map[string]any{
		"expected": expected,
		"actual":   actual,
		"path":     path,
	})
}

// ErrDirCreate creates an error for directory creation failure.
func ErrDirCreate(path string, err error) *notoerr.Error {
	details := map[string]any{"path": path}
	if err != nil {
		details["cause"] = err.Error()
	}
	return notoerr.New(ErrCodeDirCreateFailed, "failed to create directory", details)
}

// ErrInvalidLayout creates an error for an invalid layout path.
func ErrInvalidLayout(reason string) *notoerr.Error {
	return notoerr.New(ErrCodeInvalidLayout, reason, nil)
}

// ErrAtomicWrite creates an error for a failed atomic write operation.
func ErrAtomicWrite(path string, err error) *notoerr.Error {
	details := map[string]any{"path": path}
	if err != nil {
		details["cause"] = err.Error()
	}
	return notoerr.New(ErrCodeAtomicWriteFailed, "atomic write failed", details)
}