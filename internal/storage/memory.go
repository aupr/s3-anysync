package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is an in-memory Storage for tests. Set NoConditional to emulate a
// provider that silently ignores If-None-Match and If-Match.
type Memory struct {
	mu            sync.Mutex
	objects       map[string]memObject
	seq           int
	NoConditional bool
}

var _ Storage = (*Memory)(nil)

type memObject struct {
	body     []byte
	meta     map[string]string
	etag     string
	modified time.Time
}

// NewMemory returns an empty store.
func NewMemory() *Memory { return &Memory{objects: map[string]memObject{}} }

func copyMeta(meta map[string]string) map[string]string {
	out := make(map[string]string, len(meta))
	for k, v := range meta {
		out[strings.ToLower(k)] = v
	}
	return out
}

func (o memObject) info(key string) Object {
	return Object{Key: key, Size: int64(len(o.body)), ETag: o.etag, LastModified: o.modified, Metadata: copyMeta(o.meta)}
}

// store must be called with mu held.
func (m *Memory) store(key string, body []byte, meta map[string]string) Object {
	m.seq++
	o := memObject{body: append([]byte(nil), body...), meta: copyMeta(meta), etag: fmt.Sprintf("\"%d\"", m.seq), modified: time.Now().UTC()}
	m.objects[key] = o
	return o.info(key)
}

func (m *Memory) Put(_ context.Context, key string, body []byte, meta map[string]string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store(key, body, meta), nil
}

func (m *Memory) PutIfAbsent(_ context.Context, key string, body []byte) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[key]; ok && !m.NoConditional {
		return Object{}, fmt.Errorf("%w: %s exists", ErrPreconditionFailed, key)
	}
	return m.store(key, body, nil), nil
}

func (m *Memory) PutIfMatch(_ context.Context, key string, body []byte, etag string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.NoConditional {
		if o, ok := m.objects[key]; !ok || o.etag != etag {
			return Object{}, fmt.Errorf("%w: %s changed", ErrPreconditionFailed, key)
		}
	}
	return m.store(key, body, nil), nil
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return nil, Object{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return append([]byte(nil), o.body...), o.info(key), nil
}

func (m *Memory) Head(_ context.Context, key string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return Object{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return o.info(key), nil
}

func (m *Memory) List(_ context.Context, prefix string) ([]Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Object
	for k, o := range m.objects {
		if strings.HasPrefix(k, prefix) {
			info := o.info(k)
			info.Metadata = nil
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (m *Memory) ListPrefixes(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := map[string]bool{}
	for k := range m.objects {
		if rest, ok := strings.CutPrefix(k, prefix); ok {
			if i := strings.Index(rest, "/"); i >= 0 {
				set[prefix+rest[:i+1]] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func (m *Memory) Copy(_ context.Context, srcKey, dstKey string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[srcKey]
	if !ok {
		return Object{}, fmt.Errorf("%w: %s", ErrNotFound, srcKey)
	}
	return m.store(dstKey, o.body, o.meta), nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

// Keys returns every stored key, sorted. For tests.
func (m *Memory) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.objects))
	for k := range m.objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
