// Package storage is the one way to the object storage: the Storage
// interface, one instance per bucket, with an adapter for the S3 protocol
// and an in-memory fake for tests. Nothing else imports an S3 SDK, so moving
// provider is a change of environment variables.
package storage

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is an object that isn't there.
var ErrNotFound = errors.New("object not found")

// ErrTooLarge is an object bigger than the caller is willing to read.
var ErrTooLarge = errors.New("object too large")

// Object is what the storage knows of a stored file.
type Object struct {
	Size        int64
	ContentType string
}

// Storage is one bucket.
type Storage interface {
	// PresignPut returns an address the client uploads to directly, good
	// for ttl. The content type and the exact size are signed in, so the
	// upload must be the file that was asked about.
	PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (string, error)
	// PresignGet returns an address that reads the object, good for ttl.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	// URL is the object's public address: only a public bucket has one.
	URL(key string) string
	// Get reads the object whole: ErrNotFound if it isn't there, ErrTooLarge
	// if it is over limit bytes.
	Get(ctx context.Context, key string, limit int64) ([]byte, error)
	// Put stores the object, replacing one with its key.
	Put(ctx context.Context, key, contentType, cacheControl string, data []byte) error
	// Head describes the object: ErrNotFound if it isn't there.
	Head(ctx context.Context, key string) (Object, error)
	// Delete removes the object. One that isn't there is no error, so a
	// delete can be tried again.
	Delete(ctx context.Context, key string) error
	// ListOlder returns the keys under prefix last changed before t.
	ListOlder(ctx context.Context, prefix string, before time.Time) ([]string, error)
}
