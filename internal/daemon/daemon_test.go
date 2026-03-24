package daemon

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"keeprun/internal/client"
	"keeprun/internal/config"
	"keeprun/internal/ipc"
	"keeprun/internal/storage"
	"keeprun/internal/task"
)

func TestCreateStopStartRemoveLifecycle(t *testing.T) {
	cancel := startTestServer(t)
	defer cancel()
	c := client.New()

	record, err := c.CreateTask(context.Background(), ipc.CreateTaskRequest{
		Name:            "svc",
		Argv:            []string{"/bin/sh", "-lc", "trap 'exit 0' TERM; while true; do sleep 1; done"},
		Cwd:             t.TempDir(),
		Env:             testEnv(),
		RunAfterRestart: false,
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if record.State.RuntimeState != task.StateRunning {
		t.Fatalf("expected running state, got %s", record.State.RuntimeState)
	}

	if _, err := c.CreateTask(context.Background(), ipc.CreateTaskRequest{
		Name:            "svc",
		Argv:            []string{"/bin/sh", "-lc", "sleep 1"},
		Cwd:             t.TempDir(),
		Env:             testEnv(),
		RunAfterRestart: false,
	}); err == nil {
		t.Fatalf("expected duplicate name error")
	}

	stopped, err := c.StopTask(context.Background(), record.Spec.ID)
	if err != nil {
		t.Fatalf("StopTask returned error: %v", err)
	}
	if stopped.State.RuntimeState != task.StateStopped {
		t.Fatalf("expected stopped state, got %s", stopped.State.RuntimeState)
	}

	started, err := c.StartTask(context.Background(), record.Spec.Name)
	if err != nil {
		t.Fatalf("StartTask returned error: %v", err)
	}
	if started.State.RuntimeState != task.StateRunning {
		t.Fatalf("expected running state after restart, got %s", started.State.RuntimeState)
	}

	if err := c.RemoveTask(context.Background(), record.Spec.ID, true); err != nil {
		t.Fatalf("RemoveTask returned error: %v", err)
	}
}

func TestExpiryAndLogs(t *testing.T) {
	cancel := startTestServer(t)
	defer cancel()
	c := client.New()

	record, err := c.CreateTask(context.Background(), ipc.CreateTaskRequest{
		Name:            "exp",
		Argv:            []string{"/bin/sh", "-lc", "echo out; echo err 1>&2; sleep 5"},
		Cwd:             t.TempDir(),
		Env:             testEnv(),
		Life:            "1s",
		RunAfterRestart: false,
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}

	waitForState(t, c, record.Spec.ID, task.StateExpired, 4*time.Second)

	reader, err := c.Logs(context.Background(), record.Spec.ID, false, 20)
	if err != nil {
		t.Fatalf("Logs returned error: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading logs failed: %v", err)
	}
	output := string(data)
	if !strings.Contains(output, "stdout out") || !strings.Contains(output, "stderr err") {
		t.Fatalf("unexpected logs: %s", output)
	}
}

func TestRestartRehydratesEligibleTasks(t *testing.T) {
	home := shortHome(t)
	t.Setenv("HOME", home)

	store := storage.New()
	now := time.Now()
	runnable := task.Record{
		Spec: task.Spec{
			ID:              "01TESTRUNNABLE",
			Name:            "runnable",
			Argv:            []string{"/bin/sh", "-lc", "trap 'exit 0' TERM; while true; do sleep 1; done"},
			Cwd:             t.TempDir(),
			Env:             testEnv(),
			RunAfterRestart: true,
			CreatedAt:       now,
		},
		State: task.State{
			DesiredState: task.DesiredRunning,
			RuntimeState: task.StateStopped,
		},
	}
	stopped := task.Record{
		Spec: task.Spec{
			ID:              "01TESTSTOPPED",
			Name:            "stopped",
			Argv:            []string{"/bin/sh", "-lc", "trap 'exit 0' TERM; while true; do sleep 1; done"},
			Cwd:             t.TempDir(),
			Env:             testEnv(),
			RunAfterRestart: false,
			CreatedAt:       now,
		},
		State: task.State{
			DesiredState: task.DesiredRunning,
			RuntimeState: task.StateStopped,
		},
	}
	if err := store.Save(runnable); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(stopped); err != nil {
		t.Fatal(err)
	}

	cfg := config.Builtins()
	server, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.Run(ctx)
	}()
	waitForPing(t, client.New(), 3*time.Second)
	defer cancel()

	waitForState(t, client.New(), runnable.Spec.ID, task.StateRunning, 3*time.Second)
	records, err := client.New().ListTasks(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	stoppedState := task.RuntimeState("")
	for _, record := range records {
		if record.Spec.ID == stopped.Spec.ID {
			stoppedState = record.State.RuntimeState
		}
	}
	if stoppedState != task.StateStopped {
		t.Fatalf("expected non-restart task to remain stopped, got %s", stoppedState)
	}
}

func startTestServer(t *testing.T) context.CancelFunc {
	t.Helper()
	t.Setenv("HOME", shortHome(t))
	cfg := config.Builtins()
	server, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = server.Run(ctx)
	}()
	waitForPing(t, client.New(), 3*time.Second)
	return cancel
}

func waitForPing(t *testing.T, c *client.Client, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := c.Ping(context.Background()); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon did not become reachable within %s", timeout)
}

func waitForState(t *testing.T, c *client.Client, ref string, want task.RuntimeState, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		records, err := c.ListTasks(context.Background(), false)
		if err == nil {
			for _, record := range records {
				if record.Spec.ID == ref || record.Spec.Name == ref {
					if record.State.RuntimeState == want {
						return
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach state %s within %s", ref, want, timeout)
}

func testEnv() map[string]string {
	return map[string]string{
		"PATH":  os.Getenv("PATH"),
		"HOME":  os.Getenv("HOME"),
		"SHELL": "/bin/sh",
		"USER":  os.Getenv("USER"),
	}
}

func shortHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "keeprun-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}
