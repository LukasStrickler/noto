package adapters

import "github.com/lukasstrickler/noto/internal/notoerr"

const (
	ErrCodeAdapterUnknown    = "ERR_ADAPTER_UNKNOWN"
	ErrCodeAdapterUpload     = "ERR_ADAPTER_UPLOAD"
	ErrCodeAdapterDownload   = "ERR_ADAPTER_DOWNLOAD"
	ErrCodeAdapterDelete     = "ERR_ADAPTER_DELETE"
	ErrCodeAdapterList       = "ERR_ADAPTER_LIST"
	ErrCodeAdapterPresign    = "ERR_ADAPTER_PRESIGN"
	ErrCodeAdapterHeadBucket = "ERR_ADAPTER_HEAD_BUCKET"
	ErrCodeAdapterConfig     = "ERR_ADAPTER_CONFIG"
)

func ErrUnknownBackend(backend string) *notoerr.Error {
	return notoerr.New(ErrCodeAdapterUnknown, "unknown storage backend", map[string]any{
		"backend": backend,
	})
}

func ErrUpload(key string, err error) *notoerr.Error {
	details := map[string]any{"key": key}
	if err != nil {
		details["cause"] = err.Error()
	}
	return notoerr.New(ErrCodeAdapterUpload, "upload failed", details)
}

func ErrDownload(key string, err error) *notoerr.Error {
	details := map[string]any{"key": key}
	if err != nil {
		details["cause"] = err.Error()
	}
	return notoerr.New(ErrCodeAdapterDownload, "download failed", details)
}

func ErrDelete(key string, err error) *notoerr.Error {
	details := map[string]any{"key": key}
	if err != nil {
		details["cause"] = err.Error()
	}
	return notoerr.New(ErrCodeAdapterDelete, "delete failed", details)
}

func ErrList(prefix string, err error) *notoerr.Error {
	details := map[string]any{"prefix": prefix}
	if err != nil {
		details["cause"] = err.Error()
	}
	return notoerr.New(ErrCodeAdapterList, "list failed", details)
}

func ErrPresign(key string, err error) *notoerr.Error {
	details := map[string]any{"key": key}
	if err != nil {
		details["cause"] = err.Error()
	}
	return notoerr.New(ErrCodeAdapterPresign, "presign failed", details)
}

func ErrHeadBucket(err error) *notoerr.Error {
	details := map[string]any{}
	if err != nil {
		details["cause"] = err.Error()
	}
	return notoerr.New(ErrCodeAdapterHeadBucket, "head bucket failed", details)
}

func ErrConfig(reason string) *notoerr.Error {
	return notoerr.New(ErrCodeAdapterConfig, reason, nil)
}