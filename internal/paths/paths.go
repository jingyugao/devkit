package paths

import (
	"os"
	"path/filepath"
)

const (
	appName        = "keeprun"
	configFileName = "config.toml"
)

func BaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config", appName)
	}
	return filepath.Join(home, ".config", appName)
}

func TasksDir() string {
	return filepath.Join(BaseDir(), "tasks")
}

func LogsDir() string {
	return filepath.Join(BaseDir(), "logs")
}

func RunDir() string {
	return filepath.Join(BaseDir(), "run")
}

func ConfigFile() string {
	return filepath.Join(BaseDir(), configFileName)
}

func SocketPath() string {
	return filepath.Join(RunDir(), "daemon.sock")
}

func PIDFile() string {
	return filepath.Join(RunDir(), "daemon.pid")
}

func TaskFile(taskID string) string {
	return filepath.Join(TasksDir(), taskID+".json")
}

func LogFile(taskID string) string {
	return filepath.Join(LogsDir(), taskID+".log")
}

func EnsureBaseDirs() error {
	for _, dir := range []string{BaseDir(), TasksDir(), LogsDir(), RunDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}
