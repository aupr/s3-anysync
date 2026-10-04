package ipc

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// pipeNameFor is the Windows pipe name for a user SID and data folder. The
// SID keeps another user from pre-creating the name; characters outside
// [A-Za-z0-9-] are replaced so the name is valid.
func pipeNameFor(sid, dataDir string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		}
		return '_'
	}, sid)
	sum := sha256.Sum256([]byte(filepath.Clean(dataDir)))
	return `\\.\pipe\s3-anysync-` + clean + "-" + hex.EncodeToString(sum[:])[:16]
}

// sameSID reports whether two SID strings denote the same account.
func sameSID(a, b string) bool {
	return a != "" && strings.EqualFold(a, b)
}
