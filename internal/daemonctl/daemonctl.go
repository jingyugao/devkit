package daemonctl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/jingyugao/devkit/internal/client"
	"github.com/jingyugao/devkit/internal/service"
)

func Ensure(ctx context.Context, persist bool) error {
	c := client.New()
	if err := c.Ping(ctx); err == nil {
		return nil
	}

	manager, mgrErr := service.NewManager()
	if persist {
		if mgrErr != nil {
			return mgrErr
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if err := manager.Install(exe); err != nil {
			return err
		}
		return waitForPing(ctx, c, 5*time.Second)
	}

	if mgrErr == nil {
		if status, err := manager.Status(); err == nil && status.Installed {
			if err := manager.Start(); err == nil {
				if err := waitForPing(ctx, c, 5*time.Second); err == nil {
					return nil
				}
			}
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := spawnDaemon(exe); err != nil {
		return err
	}
	return waitForPing(ctx, c, 5*time.Second)
}

func StopService() error {
	manager, err := service.NewManager()
	if err != nil {
		return err
	}
	return manager.Stop()
}

func InstallService() error {
	manager, err := service.NewManager()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return manager.Install(exe)
}

func UninstallService() error {
	manager, err := service.NewManager()
	if err != nil {
		return err
	}
	return manager.Uninstall()
}

func ServiceStatus() (service.Status, error) {
	manager, err := service.NewManager()
	if err != nil {
		return service.Status{}, err
	}
	return manager.Status()
}

func spawnDaemon(executable string) error {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer devNull.Close()

	cmd := exec.Command(executable, "daemon", "serve")
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func waitForPing(ctx context.Context, c *client.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := c.Ping(ctx); err == nil {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start within %s", timeout)
}
