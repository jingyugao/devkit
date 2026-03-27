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
	preparedKube adapter.Prepared
	execErr      error
	testErr      error
	kubeErr      error
}

func (f fakeRegistry) PrepareExec(profile.Profile, store.Secret, string, []string) (adapter.Prepared, error) {
	return f.preparedExec, f.execErr
}

func (f fakeRegistry) PrepareTest(profile.Profile, store.Secret, string) (adapter.Prepared, error) {
	return f.preparedTest, f.testErr
}

func (f fakeRegistry) PrepareKubeAggregateExec([]profile.Profile, []store.Secret, string, []string) (adapter.Prepared, error) {
	return f.preparedKube, f.kubeErr
}

func (f fakeRegistry) DefaultTool(profile.Type) (string, error) {
	return "mysql", nil
}

func (f fakeRegistry) ProfileTypeForTool(binary string) (profile.Type, error) {
	switch binary {
	case "mysql":
		return profile.TypeMySQL, nil
	case "k9s", "kubectl":
		return profile.TypeKube, nil
	case "ssh":
		return profile.TypeSSH, nil
	default:
		return "", adapter.ErrUnsupportedTool
	}
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
	} else if !strings.HasSuffix(got, "\n") {
		t.Fatalf("expected stored private key to preserve trailing newline, got %q", got)
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

func TestLSListsProfiles(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{
		"cache": {Name: "cache", Type: profile.TypeRedis, Host: "redis", Port: 6379},
	}}
	var stdout bytes.Buffer
	app := &app{
		profiles:   profiles,
		secrets:    &fakeSecretStore{items: map[string]store.Secret{}},
		adapters:   fakeRegistry{},
		stdin:      strings.NewReader(""),
		stdout:     &stdout,
		stderr:     &bytes.Buffer{},
		environ:    func() []string { return nil },
		execCmd:    exec.Command,
		cmdOutput:  nil,
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
	}

	if err := app.run([]string{"ls"}); err != nil {
		t.Fatalf("run(ls) returned error: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "cache") || !strings.Contains(got, "TYPE") {
		t.Fatalf("unexpected ls output: %q", got)
	}
}

func TestImportSSHStoresImportedProfileAndSecret(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{}}
	dir := t.TempDir()

	keyFile := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyFile, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nkey\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile+".pub", []byte("ssh-ed25519 AAAATEST ops@example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), []byte("ssh.example ssh-ed25519 AAAAHOST\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "ssh_config")
	if err := os.WriteFile(configPath, []byte("Host dev\n  HostName ssh.example\n  User ops\n  Port 2222\n  IdentityFile "+keyFile+"\n  UserKnownHostsFile "+filepath.Join(dir, "known_hosts")+"\n"), 0o600); err != nil {
		t.Fatal(err)
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

	if err := app.run([]string{"import", "ssh", "shell", "--host", "dev", "--config", configPath}); err != nil {
		t.Fatalf("run(import ssh) returned error: %v", err)
	}
	got := profiles.items["shell"]
	if got.Type != profile.TypeSSH || got.Host != "ssh.example" || got.Port != 2222 || got.Username != "ops" {
		t.Fatalf("unexpected imported ssh profile: %#v", got)
	}
	if !strings.Contains(secrets.items["shell"].PrivateKey, "OPENSSH PRIVATE KEY") {
		t.Fatalf("unexpected imported ssh secret: %#v", secrets.items["shell"])
	}
}

func TestImportSSHCommandStoresGeneratedProfileAndSecret(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{}}
	dir := t.TempDir()

	keyPath := filepath.Join(dir, "yuebai")
	if err := os.WriteFile(keyPath, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nkey\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath+".pub", []byte("ssh-rsa AAAATEST ops@example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	knownHostsPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHostsPath, []byte("aliyun.gaojingyu.site ssh-ed25519 AAAAHOST\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := &app{
		profiles: profiles,
		secrets:  secrets,
		adapters: fakeRegistry{},
		stdin:    strings.NewReader(""),
		stdout:   &bytes.Buffer{},
		stderr:   &bytes.Buffer{},
		environ:  func() []string { return nil },
		execCmd:  exec.Command,
		cmdOutput: func(name string, args ...string) ([]byte, error) {
			if name != "ssh" {
				t.Fatalf("unexpected command name: %q", name)
			}
			if len(args) == 0 || args[0] != "-G" {
				t.Fatalf("expected ssh -G invocation, got %#v", args)
			}
			return []byte("user wsl\nhostname aliyun.gaojingyu.site\nport 23456\nidentityfile " + keyPath + "\nuserknownhostsfile " + knownHostsPath + "\n"), nil
		},
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
	}

	if err := app.run([]string{"import", "ssh", "wsl@aliyun.gaojingyu.site", "-oPort=23456"}); err != nil {
		t.Fatalf("run(import ssh raw target) returned error: %v", err)
	}
	got := profiles.items["wsl.aliyun.gaojingyu.site"]
	if got.Type != profile.TypeSSH || got.Host != "aliyun.gaojingyu.site" || got.Port != 23456 || got.Username != "wsl" {
		t.Fatalf("unexpected imported ssh profile: %#v", got)
	}
	if !strings.Contains(secrets.items["wsl.aliyun.gaojingyu.site"].PrivateKey, "OPENSSH PRIVATE KEY") {
		t.Fatalf("unexpected imported ssh secret: %#v", secrets.items["wsl.aliyun.gaojingyu.site"])
	}
}

func TestImportKubeStoresImportedExecProfile(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{}}
	dir := t.TempDir()

	kubeconfig := `apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: dev-cluster
  cluster:
    server: https://k8s.example:6443
contexts:
- name: dev
  context:
    cluster: dev-cluster
    user: dev-user
users:
- name: dev-user
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: aws
      args:
      - eks
      - get-token
`
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(configPath, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
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

	if err := app.run([]string{"import", "kube", "cluster", "--kubeconfig", configPath}); err != nil {
		t.Fatalf("run(import kube) returned error: %v", err)
	}
	got := profiles.items["cluster"]
	if got.Type != profile.TypeKube || got.Server != "https://k8s.example:6443" || got.ExecCommand != "aws" || len(got.ExecArgs) != 2 {
		t.Fatalf("unexpected imported kube profile: %#v", got)
	}
	if secret := secrets.items["cluster"]; secret.Token != "" || secret.ClientKey != "" {
		t.Fatalf("expected empty stored secret for exec auth, got %#v", secret)
	}
}

func TestImportMySQLStoresImportedLoginPathProfile(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{}}

	app := &app{
		profiles: profiles,
		secrets:  secrets,
		adapters: fakeRegistry{},
		stdin:    strings.NewReader(""),
		stdout:   &bytes.Buffer{},
		stderr:   &bytes.Buffer{},
		environ:  func() []string { return nil },
		execCmd:  exec.Command,
		cmdOutput: func(name string, args ...string) ([]byte, error) {
			return []byte("[doris]\nuser = root\nhost = 127.0.0.1\nport = 9030\n"), nil
		},
		isTTY: func() bool { return false },
		readSecret: func() (string, error) {
			return "", nil
		},
	}

	if err := app.run([]string{"import", "mysql", "doris", "--login-path", "doris"}); err != nil {
		t.Fatalf("run(import mysql) returned error: %v", err)
	}
	got := profiles.items["doris"]
	if got.Type != profile.TypeMySQL || got.MySQLLoginPath != "doris" || got.Username != "root" || got.Host != "127.0.0.1" || got.Port != 9030 {
		t.Fatalf("unexpected imported mysql profile: %#v", got)
	}
	if secret := secrets.items["doris"]; secret != (store.Secret{}) {
		t.Fatalf("expected empty mysql secret, got %#v", secret)
	}
}

func TestImportAllImportsSSHKubeAndMySQL(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{}}
	dir := t.TempDir()

	keyFile := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyFile, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nkey\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), []byte("ssh.example ssh-ed25519 AAAAHOST\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sshConfig := filepath.Join(dir, "ssh_config")
	if err := os.WriteFile(sshConfig, []byte("Host dev\n  HostName ssh.example\n  User ops\n  Port 2222\n  IdentityFile "+keyFile+"\n  UserKnownHostsFile "+filepath.Join(dir, "known_hosts")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	kubeconfig := `apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: dev-cluster
  cluster:
    server: https://k8s.example:6443
contexts:
- name: dev
  context:
    cluster: dev-cluster
    user: dev-user
users:
- name: dev-user
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: aws
      args:
      - eks
      - get-token
`
	kubeconfigPath := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}

	app := &app{
		profiles: profiles,
		secrets:  secrets,
		adapters: fakeRegistry{},
		stdin:    strings.NewReader(""),
		stdout:   &bytes.Buffer{},
		stderr:   &bytes.Buffer{},
		environ:  func() []string { return nil },
		execCmd:  exec.Command,
		cmdOutput: func(name string, args ...string) ([]byte, error) {
			return []byte("[doris]\nuser = root\nhost = 127.0.0.1\nport = 9030\n"), nil
		},
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
	}

	err := app.run([]string{
		"import",
		"all",
		"--ssh", "shell:dev",
		"--ssh-config", sshConfig,
		"--kube", "cluster:dev",
		"--kubeconfig", kubeconfigPath,
		"--mysql", "doris",
		"--login-path", "doris",
	})
	if err != nil {
		t.Fatalf("run(import all) returned error: %v", err)
	}
	if len(profiles.items) != 3 {
		t.Fatalf("expected 3 imported profiles, got %#v", profiles.items)
	}
	if profiles.items["doris"].MySQLLoginPath != "doris" || profiles.items["cluster"].ExecCommand != "aws" || profiles.items["shell"].Host != "ssh.example" {
		t.Fatalf("unexpected imported profiles: %#v", profiles.items)
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

func TestToolShortcutExecutesUniqueMatchingProfile(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{
		"bigdata": {Name: "bigdata", Type: profile.TypeKube},
	}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{
		"bigdata": {},
	}}

	var stdout bytes.Buffer
	app := &app{
		profiles: profiles,
		secrets:  secrets,
		adapters: fakeRegistry{
			preparedExec: adapter.Prepared{
				Path: "sh",
				Args: []string{"-lc", "printf '%s' shortcut-ok"},
			},
		},
		stdin:      strings.NewReader(""),
		stdout:     &stdout,
		stderr:     &bytes.Buffer{},
		environ:    func() []string { return nil },
		execCmd:    exec.Command,
		cmdOutput:  nil,
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
	}

	if err := app.run([]string{"k9s"}); err != nil {
		t.Fatalf("run(k9s) returned error: %v", err)
	}
	if got := stdout.String(); got != "shortcut-ok" {
		t.Fatalf("unexpected shortcut stdout: %q", got)
	}
}

func TestToolShortcutRejectsAmbiguousProfiles(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{
		"bigdata": {Name: "bigdata", Type: profile.TypeKube},
		"common":  {Name: "common", Type: profile.TypeKube},
	}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{
		"bigdata": {},
		"common":  {},
	}}
	app := &app{
		profiles:   profiles,
		secrets:    secrets,
		adapters:   fakeRegistry{},
		stdin:      strings.NewReader(""),
		stdout:     &bytes.Buffer{},
		stderr:     &bytes.Buffer{},
		environ:    func() []string { return nil },
		execCmd:    exec.Command,
		cmdOutput:  nil,
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
	}

	err := app.run([]string{"kubectl", "get", "pods"})
	if err == nil || !strings.Contains(err.Error(), "matches multiple kube profiles") {
		t.Fatalf("expected ambiguous shortcut error, got %v", err)
	}
}

func TestToolShortcutRunsK9sAcrossMultipleKubeProfiles(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{
		"bigdata": {Name: "bigdata", Type: profile.TypeKube},
		"common":  {Name: "common", Type: profile.TypeKube},
	}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{
		"bigdata": {},
		"common":  {},
	}}

	var stdout bytes.Buffer
	app := &app{
		profiles: profiles,
		secrets:  secrets,
		adapters: fakeRegistry{
			preparedKube: adapter.Prepared{
				Path: "sh",
				Args: []string{"-lc", "printf '%s' kube-aggregate-ok"},
			},
		},
		stdin:      strings.NewReader(""),
		stdout:     &stdout,
		stderr:     &bytes.Buffer{},
		environ:    func() []string { return nil },
		execCmd:    exec.Command,
		cmdOutput:  nil,
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
	}

	if err := app.run([]string{"k9s"}); err != nil {
		t.Fatalf("run(k9s) returned error: %v", err)
	}
	if got := stdout.String(); got != "kube-aggregate-ok" {
		t.Fatalf("unexpected aggregate shortcut stdout: %q", got)
	}
}

func TestToolShortcutSSHPassthroughWithoutDestination(t *testing.T) {
	var stdout bytes.Buffer
	var gotName string
	var gotArgs []string

	app := &app{
		profiles: &fakeProfileStore{items: map[string]profile.Profile{}},
		secrets:  &fakeSecretStore{items: map[string]store.Secret{}},
		adapters: fakeRegistry{},
		stdin:    strings.NewReader(""),
		stdout:   &stdout,
		stderr:   &bytes.Buffer{},
		environ:  func() []string { return nil },
		execCmd: func(name string, args ...string) *exec.Cmd {
			gotName = name
			gotArgs = append([]string{}, args...)
			return exec.Command("sh", "-lc", "printf '%s' ssh-pass-through")
		},
		cmdOutput:  nil,
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
	}

	if err := app.run([]string{"ssh", "-V"}); err != nil {
		t.Fatalf("run(ssh -V) returned error: %v", err)
	}
	if gotName != "ssh" || len(gotArgs) != 1 || gotArgs[0] != "-V" {
		t.Fatalf("unexpected passthrough invocation: name=%q args=%#v", gotName, gotArgs)
	}
	if got := stdout.String(); got != "ssh-pass-through" {
		t.Fatalf("unexpected passthrough stdout: %q", got)
	}
}

func TestToolShortcutSSHRawTargetPassthroughsToSSH(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{
		"root.aliyun.gaojingyu.site": {
			Name:     "root.aliyun.gaojingyu.site",
			Type:     profile.TypeSSH,
			Host:     "aliyun.gaojingyu.site",
			Port:     22,
			Username: "root",
		},
	}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{
		"root.aliyun.gaojingyu.site": {PrivateKey: "key"},
	}}
	var stdout bytes.Buffer
	var gotName string
	var gotArgs []string

	app := &app{
		profiles: profiles,
		secrets:  secrets,
		adapters: fakeRegistry{},
		stdin:    strings.NewReader(""),
		stdout:   &stdout,
		stderr:   &bytes.Buffer{},
		environ: func() []string {
			return nil
		},
		execCmd: func(name string, args ...string) *exec.Cmd {
			gotName = name
			gotArgs = append([]string{}, args...)
			return exec.Command("sh", "-lc", "printf '%s' ssh-raw-pass-through")
		},
		cmdOutput:  nil,
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
	}

	if err := app.run([]string{"ssh", "root@aliyun.gaojingyu.site"}); err != nil {
		t.Fatalf("run(ssh raw target) returned error: %v", err)
	}
	if gotName != "ssh" || len(gotArgs) != 1 || gotArgs[0] != "root@aliyun.gaojingyu.site" {
		t.Fatalf("unexpected raw ssh passthrough invocation: name=%q args=%#v", gotName, gotArgs)
	}
	if got := stdout.String(); got != "ssh-raw-pass-through" {
		t.Fatalf("unexpected ssh shortcut stdout: %q", got)
	}
}

func TestToolShortcutSSHProfileNameUsesStoredProfile(t *testing.T) {
	profiles := &fakeProfileStore{items: map[string]profile.Profile{
		"root.aliyun.gaojingyu.site": {
			Name:     "root.aliyun.gaojingyu.site",
			Type:     profile.TypeSSH,
			Host:     "aliyun.gaojingyu.site",
			Port:     22,
			Username: "root",
		},
	}}
	secrets := &fakeSecretStore{items: map[string]store.Secret{
		"root.aliyun.gaojingyu.site": {PrivateKey: "key"},
	}}
	var stdout bytes.Buffer

	app := &app{
		profiles: profiles,
		secrets:  secrets,
		adapters: fakeRegistry{
			preparedExec: adapter.Prepared{
				Path: "sh",
				Args: []string{"-lc", "printf '%s' ssh-profile-match"},
			},
		},
		stdin:      strings.NewReader(""),
		stdout:     &stdout,
		stderr:     &bytes.Buffer{},
		environ:    func() []string { return nil },
		execCmd:    exec.Command,
		cmdOutput:  nil,
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
	}

	if err := app.run([]string{"ssh", "root.aliyun.gaojingyu.site"}); err != nil {
		t.Fatalf("run(ssh profile target) returned error: %v", err)
	}
	if got := stdout.String(); got != "ssh-profile-match" {
		t.Fatalf("unexpected ssh profile shortcut stdout: %q", got)
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
