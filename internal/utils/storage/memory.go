package storage

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"
)

// Memory is an in-memory bucket for tests: they need neither the network nor
// credentials.
type Memory struct {
	name    string
	mu      sync.Mutex
	objects map[string]memoryObject
}

type memoryObject struct {
	data                      []byte
	contentType, cacheControl string
	changed                   time.Time
}

// NewMemory returns an empty bucket; name appears in its addresses.
func NewMemory(name string) *Memory {
	return &Memory{name: name, objects: map[string]memoryObject{}}
}

// PresignPut implements Storage.
func (m *Memory) PresignPut(_ context.Context, key, _ string, _ int64, _ time.Duration) (string, error) {
	return "https://" + m.name + ".test/" + key + "?signed=put", nil
}

// PresignGet implements Storage.
func (m *Memory) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://" + m.name + ".test/" + key + "?signed=get", nil
}

// URL implements Storage.
func (m *Memory) URL(key string) string {
	return "https://" + m.name + ".test/" + key
}

// Get implements Storage.
func (m *Memory) Get(_ context.Context, key string, limit int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	if int64(len(obj.data)) > limit {
		return nil, ErrTooLarge
	}
	return obj.data, nil
}

// Put implements Storage.
func (m *Memory) Put(_ context.Context, key, contentType, cacheControl string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memoryObject{data: data, contentType: contentType, cacheControl: cacheControl, changed: time.Now()}
	return nil
}

// Head implements Storage.
func (m *Memory) Head(_ context.Context, key string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return Object{}, ErrNotFound
	}
	return Object{Size: int64(len(obj.data)), ContentType: obj.contentType}, nil
}

// Delete implements Storage.
func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

// ListOlder implements Storage.
func (m *Memory) ListOlder(_ context.Context, prefix string, before time.Time) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for key, obj := range m.objects {
		if strings.HasPrefix(key, prefix) && obj.changed.Before(before) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys, nil
}

// Keys returns every key in the bucket, sorted, for tests to look at.
func (m *Memory) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.objects))
	for key := range m.objects {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// CacheControl returns the header the object was stored with.
func (m *Memory) CacheControl(key string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.objects[key].cacheControl
}

// Age makes the object look as if it last changed d ago.
func (m *Memory) Age(key string, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj := m.objects[key]
	obj.changed = time.Now().Add(-d)
	m.objects[key] = obj
}
