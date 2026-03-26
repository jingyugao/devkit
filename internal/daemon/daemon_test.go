package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jingyugao/devkit/internal/client"
	"github.com/jingyugao/devkit/internal/config"
	"github.com/jingyugao/devkit/internal/ipc"
	"github.com/jingyugao/devkit/internal/storage"
	"github.com/jingyugao/devkit/internal/task"
)

func TestCreateStopStartRemoveLifecycle(t *testing.T) {
	cancel := startTestServer(t)
	defer cancel()
	c := client.New()

	record, err := c.CreateTask(context.Background(), ipc.CreateTaskRequest{
		Name: "svc",
		Argv: []string{"/bin/sh", "-lc", "trap 'exit 0' TERM; while true; do sleep 1; done"},
		Cwd:  t.TempDir(),
		Env:  testEnv(),
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if record.State.RuntimeState != task.StateRunning {
		t.Fatalf("expected running state, got %s", record.State.RuntimeState)
	}
	if len(record.Spec.ID) != 6 {
		t.Fatalf("expected 6-char task id, got %q", record.Spec.ID)
	}

	if _, err := c.CreateTask(context.Background(), ipc.CreateTaskRequest{
		Name: "svc",
		Argv: []string{"/bin/sh", "-lc", "sleep 1"},
		Cwd:  t.TempDir(),
		Env:  testEnv(),
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
	if started.State.RestartCount < 1 {
		t.Fatalf("expected restart count to increment after manual start, got %d", started.State.RestartCount)
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
		Name: "exp",
		Argv: []string{"/bin/sh", "-lc", "echo out; echo err 1>&2; sleep 5"},
		Cwd:  t.TempDir(),
		Env:  testEnv(),
		Life: "1s",
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

func TestExitAutoRestartsTask(t *testing.T) {
	cancel := startTestServer(t)
	defer cancel()
	c := client.New()

	record, err := c.CreateTask(context.Background(), ipc.CreateTaskRequest{
		Name: "oneshot",
		Argv: []string{"/bin/sh", "-lc", "echo once"},
		Cwd:  t.TempDir(),
		Env:  testEnv(),
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}

	waitForRestartCountAtLeast(t, c, record.Spec.ID, 1, 4*time.Second)
	updated, err := resolveTask(c, record.Spec.ID)
	if err != nil {
		t.Fatalf("resolveTask returned error: %v", err)
	}
	if updated.State.DesiredState != task.DesiredRunning {
		t.Fatalf("expected desired state to remain running, got %s", updated.State.DesiredState)
	}
	if updated.State.RestartCount < 1 {
		t.Fatalf("expected restart count to increment, got %d", updated.State.RestartCount)
	}
	if updated.State.RuntimeState == task.StateStopped || updated.State.RuntimeState == task.StateFailed || updated.State.RuntimeState == task.StateExpired {
		t.Fatalf("expected task to stay restartable after exit, got %s", updated.State.RuntimeState)
	}
}

func TestRestartRehydratesDesiredRunningTasks(t *testing.T) {
	home := shortHome(t)
	t.Setenv("HOME", home)

	store := storage.New()
	now := time.Now()
	runnable := task.Record{
		Spec: task.Spec{
			ID:        "run123",
			Name:      "runnable",
			Argv:      []string{"/bin/sh", "-lc", "trap 'exit 0' TERM; while true; do sleep 1; done"},
			Cwd:       t.TempDir(),
			Env:       testEnv(),
			CreatedAt: now,
		},
		State: task.State{
			DesiredState: task.DesiredRunning,
			RuntimeState: task.StateStopped,
		},
	}
	stopped := task.Record{
		Spec: task.Spec{
			ID:        "stop12",
			Name:      "stopped",
			Argv:      []string{"/bin/sh", "-lc", "trap 'exit 0' TERM; while true; do sleep 1; done"},
			Cwd:       t.TempDir(),
			Env:       testEnv(),
			CreatedAt: now,
		},
		State: task.State{
			DesiredState: task.DesiredStopped,
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

	waitForState(t, client.New(), runnable.Spec.ID, task.StateRunning, 3*time.Second)
	updatedStopped, err := resolveTask(client.New(), stopped.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedStopped.State.RuntimeState != task.StateStopped {
		t.Fatalf("expected desired-stopped task to remain stopped, got %s", updatedStopped.State.RuntimeState)
	}
}

func TestStartAllStartsStoppedTasks(t *testing.T) {
	cancel := startTestServer(t)
	defer cancel()
	c := client.New()

	first, err := c.CreateTask(context.Background(), ipc.CreateTaskRequest{
		Name: "first",
		Argv: []string{"/bin/sh", "-lc", "trap 'exit 0' TERM; while true; do sleep 1; done"},
		Cwd:  t.TempDir(),
		Env:  testEnv(),
	})
	if err != nil {
		t.Fatalf("CreateTask(first) returned error: %v", err)
	}
	second, err := c.CreateTask(context.Background(), ipc.CreateTaskRequest{
		Name: "second",
		Argv: []string{"/bin/sh", "-lc", "trap 'exit 0' TERM; while true; do sleep 1; done"},
		Cwd:  t.TempDir(),
		Env:  testEnv(),
	})
	if err != nil {
		t.Fatalf("CreateTask(second) returned error: %v", err)
	}
	if _, err := c.StopTask(context.Background(), first.Spec.ID); err != nil {
		t.Fatalf("StopTask(first) returned error: %v", err)
	}
	if _, err := c.StopTask(context.Background(), second.Spec.ID); err != nil {
		t.Fatalf("StopTask(second) returned error: %v", err)
	}

	started, err := c.StartAll(context.Background())
	if err != nil {
		t.Fatalf("StartAll returned error: %v", err)
	}
	if len(started) != 2 {
		t.Fatalf("expected 2 tasks from StartAll, got %d", len(started))
	}
	waitForState(t, c, first.Spec.ID, task.StateRunning, 3*time.Second)
	waitForState(t, c, second.Spec.ID, task.StateRunning, 3*time.Second)
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
		record, err := resolveTask(c, ref)
		if err == nil && record.State.RuntimeState == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach state %s within %s", ref, want, timeout)
}

func waitForRestartCountAtLeast(t *testing.T, c *client.Client, ref string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		record, err := resolveTask(c, ref)
		if err == nil && record.State.RestartCount >= want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach restart count %d within %s", ref, want, timeout)
}

func resolveTask(c *client.Client, ref string) (task.Record, error) {
	records, err := c.ListTasks(context.Background(), false)
	if err != nil {
		return task.Record{}, err
	}
	for _, record := range records {
		if record.Spec.ID == ref || record.Spec.Name == ref {
			return record, nil
		}
	}
	return task.Record{}, errors.New("task not found")
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
