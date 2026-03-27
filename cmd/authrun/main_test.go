package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jingyugao/devkit/internal/authrun/adapter"
	"github.com/jingyugao/devkit/internal/authrun/profile"
	"github.com/jingyugao/devkit/internal/authrun/store"
)

type fakeProfileStore struct {
	items map[string]profile.Profile
}

func (s *fakeProfileStore) Add(p profile.Profile) error {
	if s.items == nil {
		s.items = map[string]profile.Profile{}
	}
	if _, exists := s.items[p.Name]; exists {
		return profile.ErrAlreadyExists
	}
	s.items[p.Name] = p
	return nil
}

func (s *fakeProfileStore) Get(name string) (profile.Profile, error) {
	p, ok := s.items[name]
	if !ok {
		return profile.Profile{}, profile.ErrNotFound
	}
	return p, nil
}

func (s *fakeProfileStore) List() ([]profile.Profile, error) {
	var out []profile.Profile
	for _, item := range s.items {
		out = append(out, item)
	}
	return out, nil
}

func (s *fakeProfileStore) Delete(name string) error {
	if _, ok := s.items[name]; !ok {
		return profile.ErrNotFound
	}
	delete(s.items, name)
	return nil
}

type fakeSecretStore struct {
	items map[string]store.Secret
}

func (s *fakeSecretStore) Put(_ context.Context, name string, secret store.Secret) error {
	if s.items == nil {
		s.items = map[string]store.Secret{}
	}
	s.items[name] = secret
	return nil
}

func (s *fakeSecretStore) Get(_ context.Context, name string) (store.Secret, error) {
	secret, ok := s.items[name]
	if !ok {
		return store.Secret{}, store.ErrNotFound
	}
	return secret, nil
}

func (s *fakeSecretStore) Delete(_ context.Context, name string) error {
	delete(s.items, name)
	return nil
}

type fakeRegistry struct {
	preparedExec adapter.Prepared
	preparedTest adapter.Prepared
	execErr      error
	testErr      error
}

func (f fakeRegistry) PrepareExec(profile.Profile, store.Secret, string, []string) (adapter.Prepared, error) {
	return f.preparedExec, f.execErr
}

func (f fakeRegistry) PrepareTest(profile.Profile, store.Secret, string) (adapter.Prepared, error) {
	return f.preparedTest, f.testErr
}

func (f fakeRegistry) DefaultTool(profile.Type) (string, error) {
	return "mysql", nil
}

func TestAddStoresProfileAndSecret(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{}}
	var stdout bytes.Buffer

	app := &app{
		profiles: profiles,
		secrets:  secrets,
		adapters: fakeRegistry{},
		stdin:    strings.NewReader(""),
		stdout:   &stdout,
		stderr:   &bytes.Buffer{},
		environ:  func() []string { return nil },
		execCmd:  exec.Command,
		isTTY:    func() bool { return true },
		readSecret: func() (string, error) {
			return "pw", nil
		},
	}

	if err := app.run([]string{"add", "prod", "--type", "mysql", "--host", "db.example", "--username", "root"}); err != nil {
		t.Fatalf("run(add) returned error: %v", err)
	}
	if _, ok := profiles.items["prod"]; !ok {
		t.Fatal("expected profile to be stored")
	}
	if got := secrets.items["prod"].Password; got != "pw" {
		t.Fatalf("unexpected stored secret: %q", got)
	}
}

func TestAddStoresSSHProfileAndKeyMaterial(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{}}

	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyFile, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nkey\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	app := &app{
		profiles:   profiles,
		secrets:    secrets,
		adapters:   fakeRegistry{},
		stdin:      strings.NewReader(""),
		stdout:     &bytes.Buffer{},
		stderr:     &bytes.Buffer{},
		environ:    func() []string { return nil },
		execCmd:    exec.Command,
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
	}

	if err := app.run([]string{"add", "shell", "--type", "ssh", "--host", "ssh.example", "--username", "ops", "--private-key-file", keyFile}); err != nil {
		t.Fatalf("run(add ssh) returned error: %v", err)
	}
	if got := secrets.items["shell"].PrivateKey; !strings.Contains(got, "OPENSSH PRIVATE KEY") {
		t.Fatalf("unexpected stored private key: %q", got)
	}
}

func TestAddStoresKubeTokenProfile(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{}}
	app := &app{
		profiles: profiles,
		secrets:  secrets,
		adapters: fakeRegistry{},
		stdin:    strings.NewReader(""),
		stdout:   &bytes.Buffer{},
		stderr:   &bytes.Buffer{},
		environ: func() []string {
			return []string{"KUBE_TOKEN=tok"}
		},
		execCmd: exec.Command,
		isTTY:   func() bool { return false },
		readSecret: func() (string, error) {
			return "", nil
		},
	}
	t.Setenv("KUBE_TOKEN", "tok")

	if err := app.run([]string{"add", "cluster", "--type", "kube", "--server", "https://k8s.example:6443", "--namespace", "dev", "--secret-env", "KUBE_TOKEN"}); err != nil {
		t.Fatalf("run(add kube) returned error: %v", err)
	}
	if got := secrets.items["cluster"].Token; got != "tok" {
		t.Fatalf("unexpected stored token: %q", got)
	}
}

func TestExecForwardsIOExitCodeAndCleanup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-tool.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nread line\necho \"out:$AUTHRUN_TEST:$1:$line\"\necho \"err:$2\" 1>&2\nexit 7\n"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	cleanupFile := filepath.Join(dir, "cleanup.tmp")
	if err := os.WriteFile(cleanupFile, []byte("temp"), 0o600); err != nil {
		t.Fatalf("WriteFile(cleanup) returned error: %v", err)
	}

	profiles := &fakeProfileStore{items: map[string]profile.Profile{
		"prod": {Name: "prod", Type: profile.TypeMySQL},
	}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{"prod": {Password: "pw"}}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	app := &app{
		profiles: profiles,
		secrets:  secrets,
		adapters: fakeRegistry{
			preparedExec: adapter.Prepared{
				Path: script,
				Args: []string{"ARG1", "ARG2"},
				Env:  []string{"AUTHRUN_TEST=ok"},
				Cleanup: func() error {
					return os.Remove(cleanupFile)
				},
			},
		},
		stdin:   strings.NewReader("payload\n"),
		stdout:  &stdout,
		stderr:  &stderr,
		environ: func() []string { return nil },
		execCmd: exec.Command,
		isTTY:   func() bool { return false },
		readSecret: func() (string, error) {
			return "", errors.New("unexpected")
		},
	}

	err := app.run([]string{"exec", "prod", "--", "mysql", "--flag"})
	if err == nil {
		t.Fatal("expected exec error from child exit code")
	}
	if code := exitCode(err); code != 7 {
		t.Fatalf("expected exit code 7, got %d", code)
	}
	if got := strings.TrimSpace(stdout.String()); got != "out:ok:ARG1:payload" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := strings.TrimSpace(stderr.String()); got != "err:ARG2" {
		t.Fatalf("unexpected stderr: %q", got)
	}
	if _, err := os.Stat(cleanupFile); !os.IsNotExist(err) {
		t.Fatalf("expected cleanup file removal, got %v", err)
	}
}

func TestRemoveDeletesProfileAndSecret(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{"prod": {Name: "prod", Type: profile.TypeRedis}}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{"prod": {Password: "pw"}}}

	app := &app{
		profiles:   profiles,
		secrets:    secrets,
		adapters:   fakeRegistry{},
		stdin:      strings.NewReader(""),
		stdout:     &bytes.Buffer{},
		stderr:     &bytes.Buffer{},
		environ:    func() []string { return nil },
		execCmd:    exec.Command,
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
	}

	if err := app.run([]string{"rm", "prod"}); err != nil {
		t.Fatalf("run(rm) returned error: %v", err)
	}
	if _, ok := profiles.items["prod"]; ok {
		t.Fatal("expected profile removal")
	}
	if _, ok := secrets.items["prod"]; ok {
		t.Fatal("expected secret removal")
	}
}
