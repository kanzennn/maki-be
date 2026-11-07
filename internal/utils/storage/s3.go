package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/kervzent/maki-be/internal/config"
)

// S3 is a bucket on any provider that speaks the S3 protocol.
type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
	baseURL string
}

// Open returns the public and the private bucket: nil and nil when the
// deployment has no storage configured.
func Open(cfg config.Storage) (public, private Storage) {
	if !cfg.Enabled() {
		return nil, nil
	}
	client := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: cfg.ForcePathStyle,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		// Since 2025 the SDK adds checksums by default, which some
		// S3-compatible providers refuse.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	presign := s3.NewPresignClient(client)
	return &S3{client: client, presign: presign, bucket: cfg.PublicBucket, baseURL: strings.TrimRight(cfg.PublicBaseURL, "/")},
		&S3{client: client, presign: presign, bucket: cfg.PrivateBucket}
}

// PresignPut implements Storage.
func (b *S3) PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (string, error) {
	req, err := b.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: &b.bucket, Key: &key, ContentType: &contentType, ContentLength: &size,
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign put %s: %w", key, err)
	}
	return req.URL, nil
}

// PresignGet implements Storage.
func (b *S3) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := b.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &b.bucket, Key: &key}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign get %s: %w", key, err)
	}
	return req.URL, nil
}

// URL implements Storage.
func (b *S3) URL(key string) string {
	return b.baseURL + "/" + key
}

// Get implements Storage.
func (b *S3) Get(ctx context.Context, key string, limit int64) ([]byte, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &b.bucket, Key: &key})
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, notFound(err))
	}
	defer func() { _ = out.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(out.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	return data, nil
}

// Put implements Storage.
func (b *S3) Put(ctx context.Context, key, contentType, cacheControl string, data []byte) error {
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &b.bucket, Key: &key, ContentType: &contentType, CacheControl: &cacheControl,
		Body: bytes.NewReader(data), ContentLength: aws.Int64(int64(len(data))),
	})
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

// Head implements Storage.
func (b *S3) Head(ctx context.Context, key string) (Object, error) {
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &b.bucket, Key: &key})
	if err != nil {
		return Object{}, fmt.Errorf("head %s: %w", key, notFound(err))
	}
	return Object{Size: aws.ToInt64(out.ContentLength), ContentType: aws.ToString(out.ContentType)}, nil
}

// Delete implements Storage.
func (b *S3) Delete(ctx context.Context, key string) error {
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &b.bucket, Key: &key})
	if err != nil && !errors.Is(notFound(err), ErrNotFound) {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

// ListOlder implements Storage.
func (b *S3) ListOlder(ctx context.Context, prefix string, before time.Time) ([]string, error) {
	var keys []string
	pages := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{Bucket: &b.bucket, Prefix: &prefix})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			if obj.LastModified != nil && obj.LastModified.Before(before) {
				keys = append(keys, aws.ToString(obj.Key))
			}
		}
	}
	return keys, nil
}

// notFound turns the SDK's 404, whichever type the call answers with, into
// ErrNotFound.
func notFound(err error) error {
	if re, ok := errors.AsType[*smithyhttp.ResponseError](err); ok && re.HTTPStatusCode() == http.StatusNotFound {
		return ErrNotFound
	}
	return err
}
