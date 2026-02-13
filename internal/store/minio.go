package store

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinIOBlobStore struct {
	client *minio.Client
	bucket string
}

func NewMinIOBlobStore(endpoint string, accessKey string, secretKey string, bucket string) (*MinIOBlobStore, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("minio endpoint is empty")
	}
	if strings.HasPrefix(endpoint, "http://") {
		endpoint = strings.TrimPrefix(endpoint, "http://")
	}
	if strings.HasPrefix(endpoint, "https://") {
		endpoint = strings.TrimPrefix(endpoint, "https://")
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}

	return &MinIOBlobStore{client: client, bucket: bucket}, nil
}

func (s *MinIOBlobStore) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check minio bucket %q: %w", s.bucket, err)
	}
	if exists {
		return nil
	}

	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("create minio bucket %q: %w", s.bucket, err)
	}
	return nil
}

func (s *MinIOBlobStore) PutFile(ctx context.Context, objectKey string, sourcePath string) error {
	stat, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("stat source file %q: %w", sourcePath, err)
	}

	contentType := mime.TypeByExtension(filepath.Ext(sourcePath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	_, err = s.client.FPutObject(ctx, s.bucket, objectKey, sourcePath, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("upload object %q (size=%d): %w", objectKey, stat.Size(), err)
	}
	return nil
}
