package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
)

// File is a Store kept in a JSON file (mode 0600). It is for development and
// tests only: the secrets are stored unencrypted.
type File struct {
	mu   sync.Mutex
	path string
}

// NewFile returns a store backed by the file at path (created on first Set).
func NewFile(path string) *File { return &File{path: path} }

func (f *File) load() (map[string]string, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("secrets file %s: %w", f.path, err)
	}
	return m, nil
}

func (f *File) save(m map[string]string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	fh, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = fh.Write(b)
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, f.path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func (f *File) Get(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *File) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[key] = value
	return f.save(m)
}

func (f *File) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return nil
	}
	delete(m, key)
	return f.save(m)
}
