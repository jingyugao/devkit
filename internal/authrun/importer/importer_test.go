package importer

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/jingyugao/devkit/internal/authrun/profile"
)

func TestImportSSHReadsDefaultConfigAndInclude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(filepath.Join(sshDir, "config.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte("Include ~/.ssh/config.d/*.conf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config.d", "dev.conf"), []byte("Host dev\n  HostName ssh.example.com\n  User ops\n  Port 2222\n  IdentityFile ~/.ssh/id_ed25519\n  UserKnownHostsFile ~/.ssh/known_hosts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nkey\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte("ssh-ed25519 AAAATEST ops@example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte("ssh.example.com ssh-ed25519 AAAAHOST\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	p, secret, err := ImportSSH(SSHInput{Name: "shell", Alias: "dev"})
	if err != nil {
		t.Fatalf("ImportSSH returned error: %v", err)
	}
	if p.Type != profile.TypeSSH || p.Host != "ssh.example.com" || p.Port != 2222 || p.Username != "ops" {
		t.Fatalf("unexpected imported ssh profile: %#v", p)
	}
	if secret.PrivateKey == "" || p.PublicKey == "" || p.KnownHosts == "" {
		t.Fatalf("expected imported ssh key material, got profile=%#v secret=%#v", p, secret)
	}
	if secret.PrivateKey[len(secret.PrivateKey)-1] != '\n' {
		t.Fatalf("expected imported private key to preserve trailing newline, got %q", secret.PrivateKey)
	}
}

func TestImportSSHCommandResolvesTargetAndGeneratesProfileName(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "yuebai")
	knownHostsPath := filepath.Join(dir, "known_hosts")

	if err := os.WriteFile(keyPath, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nkey\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath+".pub", []byte("ssh-rsa AAAATEST ops@example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(knownHostsPath, []byte("aliyun.gaojingyu.site ssh-ed25519 AAAAHOST\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	p, secret, err := ImportSSHCommand(SSHCommandInput{
		Target: "wsl@aliyun.gaojingyu.site",
		Args:   []string{"-oPort=23456", "-o", "IdentityFile=" + keyPath},
	}, func(name string, args ...string) ([]byte, error) {
		if name != "ssh" {
			t.Fatalf("unexpected command name: %q", name)
		}
		if len(args) == 0 || args[0] != "-G" {
			t.Fatalf("expected ssh -G invocation, got %#v", args)
		}
		return []byte("user wsl\nhostname aliyun.gaojingyu.site\nport 23456\nidentityfile " + keyPath + "\nuserknownhostsfile " + knownHostsPath + "\n"), nil
	})
	if err != nil {
		t.Fatalf("ImportSSHCommand returned error: %v", err)
	}

	if p.Name != "wsl.aliyun.gaojingyu.site" || p.Host != "aliyun.gaojingyu.site" || p.Port != 23456 || p.Username != "wsl" {
		t.Fatalf("unexpected imported ssh command profile: %#v", p)
	}
	if secret.PrivateKey == "" || p.PublicKey == "" || p.KnownHosts == "" {
		t.Fatalf("expected imported ssh command key material, got profile=%#v secret=%#v", p, secret)
	}
}

func TestImportKubeReadsTokenFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token.txt"), []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ca := base64.StdEncoding.EncodeToString([]byte("ca-data"))
	kubeconfig := `apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: dev-cluster
  cluster:
    server: https://k8s.example:6443
    certificate-authority-data: ` + ca + `
contexts:
- name: dev
  context:
    cluster: dev-cluster
    user: dev-user
    namespace: app
users:
- name: dev-user
  user:
    tokenFile: token.txt
`
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}

	p, secret, err := ImportKube(KubeInput{Name: "cluster", KubeconfigPath: path})
	if err != nil {
		t.Fatalf("ImportKube returned error: %v", err)
	}
	if p.Type != profile.TypeKube || p.Server != "https://k8s.example:6443" || p.Cluster != "dev-cluster" || p.Context != "dev" || p.Namespace != "app" {
		t.Fatalf("unexpected imported kube profile: %#v", p)
	}
	if p.CertificateAuthority != "ca-data" || secret.Token != "tok" {
		t.Fatalf("unexpected imported kube auth: profile=%#v secret=%#v", p, secret)
	}
}

func TestImportKubeReadsExecAuth(t *testing.T) {
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
      env:
      - name: AWS_PROFILE
        value: sandbox
      interactiveMode: Never
`
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}

	p, secret, err := ImportKube(KubeInput{Name: "cluster", KubeconfigPath: path})
	if err != nil {
		t.Fatalf("ImportKube returned error: %v", err)
	}
	if secret.Token != "" || secret.ClientKey != "" {
		t.Fatalf("expected exec auth to keep secret store empty, got %#v", secret)
	}
	if p.ExecCommand != "aws" || len(p.ExecArgs) != 2 || p.ExecArgs[0] != "eks" || p.ExecEnv["AWS_PROFILE"] != "sandbox" || p.ExecInteractiveMode != "Never" {
		t.Fatalf("unexpected imported exec profile: %#v", p)
	}
}

func TestImportKubeRejectsAuthProvider(t *testing.T) {
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
    auth-provider:
      name: gcp
`
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := ImportKube(KubeInput{Name: "cluster", KubeconfigPath: path}); err == nil {
		t.Fatal("expected unsupported auth-provider error")
	}
}
