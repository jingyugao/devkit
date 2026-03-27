package profile

import (
	"errors"
	"os"
	"testing"
)

func TestAddListGetDeleteProfiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := NewStore(DefaultProfilesPath(t))

	first := Profile{Name: "prod", Type: TypeMySQL, Host: "db.example", Username: "root"}
	second := Profile{Name: "cache", Type: TypeRedis, Host: "redis.example", Database: "1"}
	third := Profile{Name: "shell", Type: TypeSSH, Host: "ssh.example", Username: "ops"}
	fourth := Profile{Name: "cluster", Type: TypeKube, Server: "https://k8s.example:6443", Namespace: "dev"}

	if err := store.Add(first); err != nil {
		t.Fatalf("Add(first) returned error: %v", err)
	}
	if err := store.Add(second); err != nil {
		t.Fatalf("Add(second) returned error: %v", err)
	}
	if err := store.Add(third); err != nil {
		t.Fatalf("Add(third) returned error: %v", err)
	}
	if err := store.Add(fourth); err != nil {
		t.Fatalf("Add(fourth) returned error: %v", err)
	}
	if err := store.Add(first); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected duplicate error, got %v", err)
	}

	got, err := store.Get("prod")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Port != 3306 {
		t.Fatalf("expected mysql default port, got %d", got.Port)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 4 || list[0].Name != "cache" || list[3].Name != "shell" {
		t.Fatalf("unexpected list order: %#v", list)
	}

	if err := store.Delete("prod"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := store.Get("prod"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestValidateRejectsInvalidProfiles(t *testing.T) {
	cases := []Profile{
		{Name: "bad/name", Type: TypeMySQL, Host: "db", Username: "root"},
		{Name: "mysql", Type: TypeMySQL, Host: "", Username: "root"},
		{Name: "mongo", Type: TypeMongo, Host: "mongo", Username: ""},
		{Name: "redis", Type: TypeRedis, Host: "", Username: ""},
		{Name: "ssh", Type: TypeSSH, Host: "", Username: "root"},
		{Name: "kube", Type: TypeKube, Server: ""},
	}

	for _, tc := range cases {
		if err := tc.Validate(); err == nil {
			t.Fatalf("expected validation error for %#v", tc)
		}
	}
}

func TestLoadRoundTripTOML(t *testing.T) {
	path := DefaultProfilesPath(t)
	store := NewStore(path)

	if err := store.Add(Profile{Name: "mongo", Type: TypeMongo, Host: "127.0.0.1", Username: "app", Database: "users", AuthDatabase: "admin", TLSCAFile: "/tmp/ca.pem"}); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("expected non-empty TOML data")
	}

	reloaded := NewStore(path)
	got, err := reloaded.Get("mongo")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !got.TLS || got.Port != 27017 || got.AuthDatabase != "admin" {
		t.Fatalf("unexpected round-trip profile: %#v", got)
	}
}

func TestSSHAndKubeDefaults(t *testing.T) {
	ssh := Profile{Name: "shell", Type: TypeSSH, Host: "ssh.example", Username: "ops"}.Normalized()
	if ssh.Port != 22 {
		t.Fatalf("expected ssh default port 22, got %d", ssh.Port)
	}

	kube := Profile{Name: "cluster", Type: TypeKube, Server: "https://k8s.example:6443"}.Normalized()
	if kube.Cluster != "cluster" || kube.Context != "cluster" {
		t.Fatalf("expected kube defaults from name, got %#v", kube)
	}
}

func DefaultProfilesPath(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/profiles.toml"
}
