// Package secrets keeps S3 secret keys and action passphrases in the OS
// keychain (macOS Keychain, Windows Credential Manager).
package secrets

import (
	"errors"
	"sync"

	"github.com/zalando/go-keyring"
)

// Service is the keychain service name for every entry.
const Service = "s3-anysync"

// ErrNotFound is returned by Get when no secret is stored under the key.
var ErrNotFound = errors.New("secret not found")

// Store reads and writes secrets.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// ProfileSecretKey is the key for a profile's S3 secret access key.
func ProfileSecretKey(profileID string) string { return "profile:" + profileID }

// ActionPassphraseKey is the key for an encrypted action's passphrase.
func ActionPassphraseKey(actionID string) string { return "action:" + actionID + ":passphrase" }

// Keyring is a Store backed by the OS keychain.
type Keyring struct{}

// NewKeyring returns the OS keychain store.
func NewKeyring() Keyring { return Keyring{} }

func (Keyring) Get(key string) (string, error) {
	v, err := keyring.Get(Service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (Keyring) Set(key, value string) error { return keyring.Set(Service, key, value) }

func (Keyring) Delete(key string) error {
	err := keyring.Delete(Service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Memory is an in-process Store for tests.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

func (s *Memory) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *Memory) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

func (s *Memory) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}
