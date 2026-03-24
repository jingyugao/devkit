package main

import (
	"os"
	"path/filepath"
	"testing"

	"keeprun/internal/config"
)

func TestParseRunRequestUsesDefaultsAndOverrides(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("VIRTUAL_ENV", "/tmp/venv")

	workDir := t.TempDir()
	cfg := config.Builtins()
	cfg.Defaults.Life = "3d"
	cfg.Defaults.RunAfterRestart = true
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
	if !req.RunAfterRestart {
		t.Fatalf("expected run_after_restart to inherit config default")
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
