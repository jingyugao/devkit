package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jingyugao/devkit/internal/authrun/profile"
	"github.com/jingyugao/devkit/internal/authrun/store"
)

func TestMySQLPrepareExecWritesOptionFile(t *testing.T) {
	registry := NewRegistry()
	p := profile.Profile{Name: "db", Type: profile.TypeMySQL, Host: "127.0.0.1", Port: 3306, Username: "root", Database: "app"}

	prepared, err := registry.PrepareExec(p, store.Secret{Password: "pw"}, "mysql", []string{"-e", "SELECT 2"})
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

	prepared, err := registry.PrepareExec(p, store.Secret{Password: "pw"}, "/usr/local/bin/redis-cli", []string{"PING"})
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

	prepared, err := registry.PrepareTest(p, store.Secret{Password: "pw"}, "mongosh")
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

	if _, err := registry.PrepareExec(p, store.Secret{Password: "pw"}, "mysql", nil); err == nil {
		t.Fatal("expected tool/profile mismatch error")
	}
}

func TestSSHPrepareExecWritesIdentityAndKnownHostsFiles(t *testing.T) {
	registry := NewRegistry()
	p := profile.Profile{
		Name:       "shell",
		Type:       profile.TypeSSH,
		Host:       "ssh.example",
		Port:       22,
		Username:   "ops",
		PublicKey:  "ssh-ed25519 AAAATEST ops@example\n",
		KnownHosts: "ssh.example ssh-ed25519 AAAAHOST\n",
	}

	prepared, err := registry.PrepareExec(p, store.Secret{
		PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n",
		Passphrase: "pw",
	}, "ssh", []string{"uptime"})
	if err != nil {
		t.Fatalf("PrepareExec returned error: %v", err)
	}
	defer prepared.Cleanup()

	if !contains(prepared.Env, "SSH_ASKPASS_REQUIRE=force") {
		t.Fatalf("expected askpass env, got %#v", prepared.Env)
	}
	keyIndex := indexOf(prepared.Args, "-i")
	if keyIndex == -1 || keyIndex+1 >= len(prepared.Args) {
		t.Fatalf("expected -i identity args, got %#v", prepared.Args)
	}
	data, err := os.ReadFile(prepared.Args[keyIndex+1])
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), "OPENSSH PRIVATE KEY") {
		t.Fatalf("unexpected private key contents: %s", data)
	}
}

func TestKubePrepareExecWritesTempKubeconfig(t *testing.T) {
	registry := NewRegistry()
	p := profile.Profile{
		Name:                 "cluster",
		Type:                 profile.TypeKube,
		Server:               "https://k8s.example:6443",
		Namespace:            "dev",
		Cluster:              "dev-cluster",
		Context:              "dev-context",
		CertificateAuthority: "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----\n",
	}

	prepared, err := registry.PrepareExec(p, store.Secret{Token: "tok"}, "kubectl", []string{"get", "pods"})
	if err != nil {
		t.Fatalf("PrepareExec returned error: %v", err)
	}
	defer prepared.Cleanup()

	if len(prepared.Env) != 1 || !strings.HasPrefix(prepared.Env[0], "KUBECONFIG=") {
		t.Fatalf("expected KUBECONFIG env, got %#v", prepared.Env)
	}
	path := strings.TrimPrefix(prepared.Env[0], "KUBECONFIG=")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), "token: 'tok'") || !strings.Contains(string(data), "server: 'https://k8s.example:6443'") {
		t.Fatalf("unexpected kubeconfig: %s", data)
	}
}

func TestKubePrepareExecSupportsClientCertificateAuth(t *testing.T) {
	registry := NewRegistry()
	p := profile.Profile{
		Name:                 "cluster",
		Type:                 profile.TypeKube,
		Server:               "https://k8s.example:6443",
		Cluster:              "dev-cluster",
		Context:              "dev-context",
		ClientCertificate:    "-----BEGIN CERTIFICATE-----\nclient\n-----END CERTIFICATE-----\n",
		CertificateAuthority: "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----\n",
	}

	prepared, err := registry.PrepareExec(p, store.Secret{ClientKey: "-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----\n"}, "kubectl", []string{"get", "ns"})
	if err != nil {
		t.Fatalf("PrepareExec returned error: %v", err)
	}
	defer prepared.Cleanup()

	path := strings.TrimPrefix(prepared.Env[0], "KUBECONFIG=")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "client-certificate-data:") || !strings.Contains(content, "client-key-data:") {
		t.Fatalf("expected client certificate auth in kubeconfig: %s", content)
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
