package adapters

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrUnknownBackend = errors.New("unknown storage backend")

type ObjectMeta struct {
	Key          string
	Size         int64
	LastModified time.Time
}

type BucketMeta struct {
	Region string
}

type PutOptions struct {
	ContentType string
	Metadata    map[string]string
}

type SyncAdapter interface {
	PutObject(ctx context.Context, key string, body io.Reader, opts PutOptions) error
	GetObject(ctx context.Context, key string, dest io.WriterAt) error
	DeleteObject(ctx context.Context, key string) error
	ListObjects(ctx context.Context, prefix string) ([]ObjectMeta, error)
	GetPresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	HeadBucket(ctx context.Context) (BucketMeta, error)
}

func NewSyncAdapter(backend string) (SyncAdapter, error) {
	switch backend {
	case "s3":
		return newS3Adapter()
	case "r2":
		return newR2Adapter()
	case "local":
		return newLocalAdapter()
	default:
		return nil, ErrUnknownBackend
	}
}

type Closeable interface {
	Close() error
}