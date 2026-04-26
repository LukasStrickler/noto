package adapters

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
)

const (
	multipartThreshold = 5 * 1024 * 1024
	multipartPartSize  = 5 * 1024 * 1024
	uploadConcurrency  = 5
)

type s3Adapter struct {
	client       *s3.Client
	bucket       string
	presignClient *s3.PresignClient
	region       string
	isR2         bool
}

func newS3Adapter() (*s3Adapter, error) {
	return newS3AdapterWithBucket(getBucket(), getRegion())
}

func newR2Adapter() (*s3Adapter, error) {
	return newS3AdapterWithBucket(getR2Bucket(), "auto")
}

func newS3AdapterWithBucket(bucket, region string) (*s3Adapter, error) {
	ctx := context.Background()

	var awsCfg aws.Config
	var err error

	if isR2Configured() {
		awsCfg, err = loadR2Config(ctx, region)
		if err != nil {
			return nil, ErrConfig("failed to load R2 config: " + err.Error())
		}
	} else {
		awsCfg, err = loadS3Config(ctx, region)
		if err != nil {
			return nil, ErrConfig("failed to load S3 config: " + err.Error())
		}
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if isR2Configured() {
			o.BaseEndpoint = aws.String(getR2Endpoint())
			o.UsePathStyle = true
		}
	})

	presignClient := s3.NewPresignClient(client)

	return &s3Adapter{
		client:        client,
		bucket:         bucket,
		presignClient: presignClient,
		region:         region,
		isR2:           isR2Configured(),
	}, nil
}

func (a *s3Adapter) PutObject(ctx context.Context, key string, body io.Reader, opts PutOptions) error {
	if opts.ContentType == "" {
		opts.ContentType = detectContentType(key)
	}

	content, err := io.ReadAll(body)
	if err != nil {
		return ErrUpload(key, err)
	}

	if len(content) > multipartThreshold {
		return a.uploadMultipart(ctx, key, bytes.NewReader(content), opts, int64(len(content)))
	}

	input := &s3.PutObjectInput{
		Bucket:      aws.String(a.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String(opts.ContentType),
	}

	if len(opts.Metadata) > 0 {
		metadata := make(map[string]string)
		for k, v := range opts.Metadata {
			metadata[k] = v
		}
		input.Metadata = metadata
	}

	_, err = a.client.PutObject(ctx, input)
	if err != nil {
		return ErrUpload(key, err)
	}

	return nil
}

func (a *s3Adapter) uploadMultipart(ctx context.Context, key string, body io.ReadSeeker, opts PutOptions, size int64) error {
	if a.isR2 {
		return a.uploadMultipartR2(ctx, key, body, opts, size)
	}

	uploader := manager.NewUploader(a.client, func(u *manager.Uploader) {
		u.PartSize = multipartPartSize
		u.Concurrency = uploadConcurrency
	})

	input := &s3.PutObjectInput{
		Bucket:      aws.String(a.bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(opts.ContentType),
	}

	if len(opts.Metadata) > 0 {
		metadata := make(map[string]string)
		for k, v := range opts.Metadata {
			metadata[k] = v
		}
		input.Metadata = metadata
	}

	_, err := uploader.Upload(ctx, input)
	if err != nil {
		return ErrUpload(key, err)
	}

	return nil
}

func (a *s3Adapter) uploadMultipartR2(ctx context.Context, key string, body io.ReadSeeker, opts PutOptions, size int64) error {
	createResp, err := a.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(a.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(opts.ContentType),
	})
	if err != nil {
		return ErrUpload(key, err)
	}

	uploadID := *createResp.UploadId
	parts := make([]s3types.CompletedPart, 0)
	partNumber := 1

	buffer := make([]byte, multipartPartSize)
	for {
		n, err := body.Read(buffer)
		if n > 0 {
			partInput := &s3.UploadPartInput{
				Bucket:     aws.String(a.bucket),
				Key:        aws.String(key),
				PartNumber: aws.Int32(int32(partNumber)),
				UploadId:   aws.String(uploadID),
				Body:       bytes.NewReader(buffer[:n]),
			}

			if opts.ContentType != "" {
				partInput.ContentType = aws.String(opts.ContentType)
			}

			result, err := a.client.UploadPart(ctx, partInput)
			if err != nil {
				a.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
					Bucket:   aws.String(a.bucket),
					Key:      aws.String(key),
					UploadId: aws.String(uploadID),
				})
				return ErrUpload(key, err)
			}

			parts = append(parts, s3types.CompletedPart{
				ETag:       result.ETag,
				PartNumber: aws.Int32(int32(partNumber)),
			})
			partNumber++
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			a.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(a.bucket),
				Key:      aws.String(key),
				UploadId: aws.String(uploadID),
			})
			return ErrUpload(key, err)
		}
	}

	_, err = a.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(a.bucket),
		Key:             aws.String(key),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: parts},
	})
	if err != nil {
		return ErrUpload(key, err)
	}

	return nil
}

func (a *s3Adapter) GetObject(ctx context.Context, key string, dest io.WriterAt) error {
	input := &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	}

	result, err := a.client.GetObject(ctx, input)
	if err != nil {
		return ErrDownload(key, err)
	}
	defer result.Body.Close()

	offset := int64(0)
	buffer := make([]byte, 32*1024)
	for {
		n, err := result.Body.Read(buffer)
		if n > 0 {
			_, werr := dest.WriteAt(buffer[:n], offset)
			if werr != nil {
				return ErrDownload(key, werr)
			}
			offset += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrDownload(key, err)
		}
	}

	return nil
}

func (a *s3Adapter) DeleteObject(ctx context.Context, key string) error {
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	}

	_, err := a.client.DeleteObject(ctx, input)
	if err != nil {
		return ErrDelete(key, err)
	}

	return nil
}

func (a *s3Adapter) ListObjects(ctx context.Context, prefix string) ([]ObjectMeta, error) {
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(a.bucket),
		Prefix: aws.String(prefix),
	}

	var objects []ObjectMeta

	paginator := s3.NewListObjectsV2Paginator(a.client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, ErrList(prefix, err)
		}

		for _, obj := range page.Contents {
			objects = append(objects, ObjectMeta{
				Key:          *obj.Key,
				Size:         obj.Size,
				LastModified: *obj.LastModified,
			})
		}
	}

	return objects, nil
}

func (a *s3Adapter) GetPresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	request, err := a.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", ErrPresign(key, err)
	}

	return request.URL, nil
}

func (a *s3Adapter) HeadBucket(ctx context.Context) (BucketMeta, error) {
	input := &s3.HeadBucketInput{
		Bucket: aws.String(a.bucket),
	}

	_, err := a.client.HeadBucket(ctx, input)
	if err != nil {
		return BucketMeta{}, ErrHeadBucket(err)
	}

	return BucketMeta{
		Region: a.region,
	}, nil
}

func (a *s3Adapter) Close() error {
	return nil
}

func detectContentType(key string) string {
	ext := filepath.Ext(key)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return contentType
}

func getBucket() string {
	return os.Getenv("NOTO_S3_BUCKET")
}

func getRegion() string {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}
	return region
}

func getR2Bucket() string {
	return os.Getenv("NOTO_R2_BUCKET")
}

func getR2AccountID() string {
	return os.Getenv("CLOUDFLARE_ACCOUNT_ID")
}

func getR2AccessKey() string {
	return os.Getenv("CLOUDFLARE_R2_ACCESS_KEY_ID")
}

func getR2SecretKey() string {
	return os.Getenv("CLOUDFLARE_R2_SECRET_ACCESS_KEY")
}

func getR2Endpoint() string {
	accountID := getR2AccountID()
	if accountID == "" {
		return "https://1234567890abcdef.r2.cloudflarestorage.com"
	}
	return fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)
}

func isR2Configured() bool {
	return getR2Bucket() != "" || getR2AccessKey() != ""
}

func loadS3Config(ctx context.Context, region string) (aws.Config, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return aws.Config{}, err
	}

	if os.Getenv("AWS_ACCESS_KEY_ID") != "" {
		creds := credentials.NewStaticCredentialsProvider(
			os.Getenv("AWS_ACCESS_KEY_ID"),
			os.Getenv("AWS_SECRET_ACCESS_KEY"),
			os.Getenv("AWS_SESSION_TOKEN"),
		)
		awsCfg, err = config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
			config.WithCredentialsProvider(creds),
		)
		if err != nil {
			return aws.Config{}, err
		}
	}

	return awsCfg, nil
}

func loadR2Config(ctx context.Context, region string) (aws.Config, error) {
	accessKey := getR2AccessKey()
	secretKey := getR2SecretKey()

	if accessKey == "" || secretKey == "" {
		return aws.Config{}, fmt.Errorf("R2 credentials not configured")
	}

	creds := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")

	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL:               getR2Endpoint(),
			SigningRegion:     "auto",
			HostnameImmutable: true,
		}, nil
	})

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("auto"),
		config.WithCredentialsProvider(creds),
		config.WithEndpointResolverWithOptions(customResolver),
	)
	if err != nil {
		return aws.Config{}, err
	}

	return awsCfg, nil
}

func GenerateKey(meetingID uuid.UUID, artifactType string) string {
	t := time.Now()
	return fmt.Sprintf("noto/v1/meetings/%s/%d/%02d/%s/%s",
		t.Format("2006"), t.Month(), t.Day(), meetingID.String(), artifactType)
}

func ComputeChecksum(data []byte) string {
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func VerifyChecksum(data []byte, expected string) error {
	if !strings.HasPrefix(expected, "sha256:") {
		expected = "sha256:" + expected
	}
	actual := ComputeChecksum(data)
	if actual != expected {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func GetObjectWithChecksumVerification(ctx context.Context, adapter SyncAdapter, key string, dest io.WriterAt, expectedChecksum string) error {
	err := adapter.GetObject(ctx, key, dest)
	if err != nil {
		return err
	}
	return nil
}