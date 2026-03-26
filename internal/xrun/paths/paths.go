package paths

import (
	"os"
	"path/filepath"
)

const appName = "xrun"

func BaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config", appName)
	}
	return filepath.Join(home, ".config", appName)
}

func ProfilesFile() string {
	return filepath.Join(BaseDir(), "profiles.toml")
}

func SecretsFile() string {
	return filepath.Join(BaseDir(), "secrets.enc")
}

func EnsureBaseDir() error {
	return os.MkdirAll(BaseDir(), 0o700)
}
