package secrets

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func exercise(t *testing.T, s Store) {
	t.Helper()
	if _, err := s.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: want ErrNotFound, got %v", err)
	}
	if err := s.Set("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get("k"); err != nil || v != "v1" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatalf("second Delete should be a no-op, got %v", err)
	}
	if _, err := s.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}

func TestMemory(t *testing.T) { exercise(t, NewMemory()) }

func TestKeyring(t *testing.T) {
	keyring.MockInit()
	exercise(t, NewKeyring())
}

func TestKeyNames(t *testing.T) {
	if ProfileSecretKey("p1") != "profile:p1" || ActionPassphraseKey("a1") != "action:a1:passphrase" {
		t.Fatal("unexpected key names")
	}
}
