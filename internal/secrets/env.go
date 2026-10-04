package secrets

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvVar selects the secrets backend: unset or "" for the OS keychain,
// "file" for an unencrypted file in the data folder (development only).
const EnvVar = "S3_ANYSYNC_SECRETS"

// FileName is the development secrets file inside the data folder.
const FileName = "secrets.json"

// FromEnv returns the store selected by EnvVar for the data folder dataDir.
func FromEnv(dataDir string) (Store, error) {
	switch v := os.Getenv(EnvVar); v {
	case "":
		return NewKeyring(), nil
	case "file":
		return NewFile(filepath.Join(dataDir, FileName)), nil
	default:
		return nil, fmt.Errorf("%s=%q: use \"file\" or leave it unset", EnvVar, v)
	}
}
