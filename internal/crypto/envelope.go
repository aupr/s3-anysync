// Package crypto encrypts file contents into a self-describing JSON envelope:
// AES-256-GCM with a scrypt-derived key (ported from the Tabby plugin's v2 format).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/crypto/scrypt"
)

// Format identifies s3-anysync envelopes.
const Format = "s3-anysync"

const (
	version    = 2
	algorithm  = "aes-256-gcm"
	kdf        = "scrypt"
	keyLength  = 32
	saltLength = 16
	ivLength   = 12
	tagLength  = 16
	scryptN    = 16384
	scryptR    = 8
	scryptP    = 1
)

var (
	// ErrAuth means the passphrase is wrong or the data was modified.
	ErrAuth = errors.New("decryption failed: wrong passphrase or tampered data")
	// ErrFormat means the payload is not an s3-anysync envelope.
	ErrFormat = errors.New("not an s3-anysync encrypted payload")
)

type envelope struct {
	Format          string `json:"format"`
	Version         int    `json:"version"`
	Algorithm       string `json:"algorithm"`
	KDF             string `json:"kdf"`
	Salt            string `json:"salt"`
	IV              string `json:"iv"`
	AuthTag         string `json:"authTag"`
	Ciphertext      string `json:"ciphertext"`
	KeyLength       int    `json:"keyLength"`
	Cost            int    `json:"cost"`
	BlockSize       int    `json:"blockSize"`
	Parallelization int    `json:"parallelization"`
}

func newGCM(passphrase string, salt []byte) (cipher.AEAD, error) {
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, keyLength)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt seals plaintext (which may be empty) with the passphrase.
func Encrypt(plaintext []byte, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("passphrase is empty")
	}
	salt := make([]byte, saltLength)
	iv := make([]byte, ivLength)
	rand.Read(salt)
	rand.Read(iv)
	gcm, err := newGCM(passphrase, salt)
	if err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, iv, plaintext, nil)
	ct, tag := sealed[:len(sealed)-tagLength], sealed[len(sealed)-tagLength:]
	b64 := base64.StdEncoding.EncodeToString
	return json.Marshal(envelope{
		Format: Format, Version: version, Algorithm: algorithm, KDF: kdf,
		Salt: b64(salt), IV: b64(iv), AuthTag: b64(tag), Ciphertext: b64(ct),
		KeyLength: keyLength, Cost: scryptN, BlockSize: scryptR, Parallelization: scryptP,
	})
}

// Decrypt opens an envelope produced by Encrypt.
func Decrypt(payload []byte, passphrase string) ([]byte, error) {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, ErrFormat
	}
	if env.Format != Format || env.Version != version || env.Algorithm != algorithm || env.KDF != kdf {
		return nil, ErrFormat
	}
	if env.KeyLength != keyLength || env.Cost != scryptN || env.BlockSize != scryptR || env.Parallelization != scryptP {
		return nil, fmt.Errorf("%w: unsupported parameters", ErrFormat)
	}
	dec := base64.StdEncoding.DecodeString
	salt, err1 := dec(env.Salt)
	iv, err2 := dec(env.IV)
	tag, err3 := dec(env.AuthTag)
	ct, err4 := dec(env.Ciphertext)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if len(salt) != saltLength || len(iv) != ivLength || len(tag) != tagLength {
		return nil, fmt.Errorf("%w: bad field lengths", ErrFormat)
	}
	gcm, err := newGCM(passphrase, salt)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, iv, append(ct, tag...), nil)
	if err != nil {
		return nil, ErrAuth
	}
	if pt == nil {
		pt = []byte{}
	}
	return pt, nil
}
