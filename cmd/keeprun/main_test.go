package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jingyugao/devkit/internal/config"
)

func TestParseRunRequestUsesDefaultsAndOverrides(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("VIRTUAL_ENV", "/tmp/venv")

	workDir := t.TempDir()
	cfg := config.Builtins()
	cfg.Defaults.Life = "3d"
	cfg.Defaults.EnvPass = []string{"VIRTUAL_ENV"}

	req, err := parseRunRequest(cfg, []string{"--env", "FOO=bar", "--cwd", workDir, "python", "httpserver.py"})
	if err != nil {
		t.Fatalf("parseRunRequest returned error: %v", err)
	}
	if req.Cwd != workDir {
		t.Fatalf("expected cwd %q, got %q", workDir, req.Cwd)
	}
	if req.Life != "3d" {
		t.Fatalf("expected life from config, got %q", req.Life)
	}
	if got := req.Env["FOO"]; got != "bar" {
		t.Fatalf("expected explicit env override, got %q", got)
	}
	if got := req.Env["VIRTUAL_ENV"]; got != "/tmp/venv" {
		t.Fatalf("expected env-pass from config, got %q", got)
	}
}

func TestParseRunRequestShorthandCommand(t *testing.T) {
	workDir := t.TempDir()
	cfg := config.Builtins()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}

	req, err := parseRunRequest(cfg, []string{"python", filepath.Join(workDir, "httpserver.py")})
	if err != nil {
		t.Fatalf("parseRunRequest returned error: %v", err)
	}
	wantCwd, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatal(err)
	}
	gotCwd, err := filepath.EvalSymlinks(req.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	if gotCwd != wantCwd {
		t.Fatalf("expected cwd %q, got %q", wantCwd, gotCwd)
	}
	if len(req.Argv) != 2 || req.Argv[0] != "python" {
		t.Fatalf("unexpected argv: %#v", req.Argv)
	}
}

func TestNormalizeInterspersedFlagsAllowsTrailingLogsFlags(t *testing.T) {
	got, err := normalizeInterspersedFlags([]string{"ticker", "--lines", "5", "-f"}, map[string]bool{"--lines": true})
	if err != nil {
		t.Fatalf("normalizeInterspersedFlags returned error: %v", err)
	}

	want := []string{"--lines", "5", "-f", "ticker"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestNormalizeInterspersedFlagsAllowsTrailingRMFlags(t *testing.T) {
	got, err := normalizeInterspersedFlags([]string{"task-id", "--force"}, nil)
	if err != nil {
		t.Fatalf("normalizeInterspersedFlags returned error: %v", err)
	}

	want := []string{"--force", "task-id"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestNormalizeInterspersedFlagsRequiresFlagValue(t *testing.T) {
	_, err := normalizeInterspersedFlags([]string{"ticker", "--lines"}, map[string]bool{"--lines": true})
	if err == nil {
		t.Fatal("expected missing flag value error")
	}
}

func TestParseRunRequestRejectsReservedCommands(t *testing.T) {
	cfg := config.Builtins()

	cases := [][]string{
		{"rm", "task"},
		{"--", "run", "task"},
		{filepath.Join("/usr/local/bin", "logs"), "task"},
	}

	for _, args := range cases {
		if _, err := parseRunRequest(cfg, args); err == nil {
			t.Fatalf("expected reserved command error for args %v", args)
		}
	}
}

func TestParseRunRequestAllowsNonReservedPathCommand(t *testing.T) {
	cfg := config.Builtins()

	req, err := parseRunRequest(cfg, []string{filepath.Join("/usr/local/bin", "worker"), "--serve"})
	if err != nil {
		t.Fatalf("parseRunRequest returned error: %v", err)
	}
	if got := req.Argv[0]; got != filepath.Join("/usr/local/bin", "worker") {
		t.Fatalf("unexpected argv[0]: %q", got)
	}
}
