package crypto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	for _, pt := range [][]byte{[]byte(`{"theme":"dark"}`), {}} {
		enc, err := Encrypt(pt, "pass")
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(enc, []byte("dark")) {
			t.Fatal("ciphertext contains plaintext")
		}
		got, err := Decrypt(enc, "pass")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pt) {
			t.Fatalf("got %q want %q", got, pt)
		}
	}
}

func TestWrongPassphrase(t *testing.T) {
	enc, _ := Encrypt([]byte("x"), "right")
	if _, err := Decrypt(enc, "wrong"); !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestTamper(t *testing.T) {
	enc, _ := Encrypt([]byte("hello world"), "p")
	var env map[string]any
	if err := json.Unmarshal(enc, &env); err != nil {
		t.Fatal(err)
	}
	ct, _ := base64.StdEncoding.DecodeString(env["ciphertext"].(string))
	ct[0] ^= 0xff
	env["ciphertext"] = base64.StdEncoding.EncodeToString(ct)
	tampered, _ := json.Marshal(env)
	if _, err := Decrypt(tampered, "p"); !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestFormatErrors(t *testing.T) {
	for _, payload := range []string{"plain text", `{"format":"other","version":2}`, `{"a":1}`} {
		if _, err := Decrypt([]byte(payload), "p"); !errors.Is(err, ErrFormat) {
			t.Errorf("Decrypt(%q): want ErrFormat, got %v", payload, err)
		}
	}
}

func TestSaltIsRandom(t *testing.T) {
	a, _ := Encrypt([]byte("x"), "p")
	b, _ := Encrypt([]byte("x"), "p")
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions produced identical output")
	}
}

func TestEmptyPassphrase(t *testing.T) {
	if _, err := Encrypt([]byte("x"), ""); err == nil {
		t.Fatal("want error for empty passphrase")
	}
}
