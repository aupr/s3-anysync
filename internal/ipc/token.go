package ipc

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

const tokenFile = "ipc.token"

// NewToken writes a fresh random token to dataDir (mode 0600) and returns it.
// On Windows the file is protected by the user profile's permissions.
func NewToken(dataDir string) (string, error) {
	b := make([]byte, 32)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	p := filepath.Join(dataDir, tokenFile)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tok, nil
}

// ReadToken returns the token the running agent wrote.
func ReadToken(dataDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, tokenFile))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
