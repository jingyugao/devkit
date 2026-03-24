package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	label       = "com.keeprun.daemon"
	linuxUnit   = "keeprund.service"
	serviceName = "keeprund.service"
)

type Status struct {
	Installed bool
	Running   bool
	Details   string
}

type Manager interface {
	Install(executable string) error
	Uninstall() error
	Start() error
	Stop() error
	Status() (Status, error)
}

func NewManager() (Manager, error) {
	switch runtime.GOOS {
	case "darwin":
		return darwinManager{}, nil
	case "linux":
		return linuxManager{}, nil
	default:
		return nil, fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}

type darwinManager struct{}

func (darwinManager) plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func (m darwinManager) Install(executable string) error {
	path, err := m.plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data := renderDarwinPlist(executable)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain, path).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("launchctl", "kickstart", "-k", domain+"/"+label).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl kickstart: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (m darwinManager) Uninstall() error {
	path, err := m.plistPath()
	if err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain, path).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (darwinManager) Start() error {
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if out, err := exec.Command("launchctl", "kickstart", "-k", domain+"/"+label).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl kickstart: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (darwinManager) Stop() error {
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	path, err := darwinManager{}.plistPath()
	if err != nil {
		return err
	}
	if out, err := exec.Command("launchctl", "bootout", domain, path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootout: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (m darwinManager) Status() (Status, error) {
	path, err := m.plistPath()
	if err != nil {
		return Status{}, err
	}
	status := Status{}
	if _, err := os.Stat(path); err == nil {
		status.Installed = true
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	out, err := exec.Command("launchctl", "print", domain+"/"+label).CombinedOutput()
	if err == nil {
		status.Running = true
	}
	status.Details = strings.TrimSpace(string(out))
	return status, nil
}

type linuxManager struct{}

func (linuxManager) unitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", linuxUnit), nil
}

func (m linuxManager) Install(executable string) error {
	path, err := m.unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(renderLinuxUnit(executable)), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %s", strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable --now: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (m linuxManager) Uninstall() error {
	path, err := m.unitPath()
	if err != nil {
		return err
	}
	_ = exec.Command("systemctl", "--user", "disable", "--now", serviceName).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (linuxManager) Start() error {
	if out, err := exec.Command("systemctl", "--user", "start", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl start: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (linuxManager) Stop() error {
	if out, err := exec.Command("systemctl", "--user", "stop", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl stop: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (m linuxManager) Status() (Status, error) {
	path, err := m.unitPath()
	if err != nil {
		return Status{}, err
	}
	status := Status{}
	if _, err := os.Stat(path); err == nil {
		status.Installed = true
	}
	out, err := exec.Command("systemctl", "--user", "is-active", serviceName).CombinedOutput()
	if err == nil && strings.TrimSpace(string(out)) == "active" {
		status.Running = true
	}
	status.Details = strings.TrimSpace(string(out))
	return status, nil
}

func renderDarwinPlist(executable string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>daemon</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
</dict>
</plist>
`, label, executable)
}

func renderLinuxUnit(executable string) string {
	return fmt.Sprintf(`[Unit]
Description=keeprun daemon

[Service]
ExecStart=%s daemon serve
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
`, executable)
}
