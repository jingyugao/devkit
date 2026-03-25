package config

import (
	"os"
	"testing"

	"github.com/jingyugao/keep-run/internal/paths"
)

func TestSaveLoadAndMutateConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := Builtins()
	if err := Set(&cfg, "defaults.life", "3d"); err != nil {
		t.Fatal(err)
	}
	if err := Set(&cfg, "defaults.env_pass", "VIRTUAL_ENV,PYENV_VERSION"); err != nil {
		t.Fatal(err)
	}
	if err := Set(&cfg, "logs.tail_lines", "50"); err != nil {
		t.Fatal(err)
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.ConfigFile()); err != nil {
		t.Fatalf("expected config file to exist: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Defaults.Life != "3d" || loaded.Logs.TailLines != 50 {
		t.Fatalf("unexpected loaded config: %#v", loaded)
	}

	if err := Unset(&loaded, "defaults.life"); err != nil {
		t.Fatal(err)
	}
	if loaded.Defaults.Life != Builtins().Defaults.Life {
		t.Fatalf("expected defaults.life to reset, got %q", loaded.Defaults.Life)
	}
}
