package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jingyugao/devkit/internal/xrun/profile"
)

func TestMySQLPrepareExecWritesOptionFile(t *testing.T) {
	registry := NewRegistry()
	p := profile.Profile{Name: "db", Type: profile.TypeMySQL, Host: "127.0.0.1", Port: 3306, Username: "root", Database: "app"}

	prepared, err := registry.PrepareExec(p, "pw", "mysql", []string{"-e", "SELECT 2"})
	if err != nil {
		t.Fatalf("PrepareExec returned error: %v", err)
	}
	defer prepared.Cleanup()

	if prepared.Path != "mysql" {
		t.Fatalf("unexpected path: %q", prepared.Path)
	}
	if len(prepared.Args) < 3 || !strings.HasPrefix(prepared.Args[0], "--defaults-extra-file=") {
		t.Fatalf("unexpected args: %#v", prepared.Args)
	}
	path := strings.TrimPrefix(prepared.Args[0], "--defaults-extra-file=")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), "password=pw") {
		t.Fatalf("option file missing password: %s", data)
	}
	if err := prepared.Cleanup(); err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected cleanup to remove temp file, got %v", err)
	}
}

func TestRedisPrepareExecSetsEnvAndArgs(t *testing.T) {
	registry := NewRegistry()
	p := profile.Profile{Name: "cache", Type: profile.TypeRedis, Host: "redis", Port: 6379, Username: "default", Database: "2", TLS: true, TLSCAFile: "/tmp/ca.pem"}

	prepared, err := registry.PrepareExec(p, "pw", "/usr/local/bin/redis-cli", []string{"PING"})
	if err != nil {
		t.Fatalf("PrepareExec returned error: %v", err)
	}
	if filepath.Base(prepared.Path) != "redis-cli" {
		t.Fatalf("unexpected path: %q", prepared.Path)
	}
	if !contains(prepared.Env, "REDISCLI_AUTH=pw") {
		t.Fatalf("expected redis auth env, got %#v", prepared.Env)
	}
	if !contains(prepared.Args, "--tls") || !contains(prepared.Args, "--cacert") {
		t.Fatalf("expected tls args, got %#v", prepared.Args)
	}
}

func TestMongoPrepareTestWritesBootstrapScript(t *testing.T) {
	registry := NewRegistry()
	p := profile.Profile{Name: "doc", Type: profile.TypeMongo, Host: "mongo", Port: 27017, Username: "app", Database: "users", AuthDatabase: "admin"}

	prepared, err := registry.PrepareTest(p, "pw", "mongosh")
	if err != nil {
		t.Fatalf("PrepareTest returned error: %v", err)
	}
	defer prepared.Cleanup()

	index := indexOf(prepared.Args, "--file")
	if index == -1 || index+1 >= len(prepared.Args) {
		t.Fatalf("expected --file arg, got %#v", prepared.Args)
	}
	data, err := os.ReadFile(prepared.Args[index+1])
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "db.auth") || !strings.Contains(content, "ping") {
		t.Fatalf("unexpected mongo bootstrap script: %s", content)
	}
}

func TestRegistryRejectsToolProfileMismatch(t *testing.T) {
	registry := NewRegistry()
	p := profile.Profile{Name: "cache", Type: profile.TypeRedis, Host: "redis", Port: 6379}

	if _, err := registry.PrepareExec(p, "pw", "mysql", nil); err == nil {
		t.Fatal("expected tool/profile mismatch error")
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func indexOf(items []string, want string) int {
	for i, item := range items {
		if item == want {
			return i
		}
	}
	return -1
}
