package store

import (
	"context"
	"errors"
	"os"
	"testing"

	xkeyring "github.com/jingyugao/devkit/internal/xrun/keyring"
)

type fakeKeys struct {
	loadKey         []byte
	loadErr         error
	loadOrCreateKey []byte
	loadOrCreateErr error
}

func (f fakeKeys) Load(context.Context) ([]byte, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	return append([]byte(nil), f.loadKey...), nil
}

func (f fakeKeys) LoadOrCreate(context.Context) ([]byte, error) {
	if f.loadOrCreateErr != nil {
		return nil, f.loadOrCreateErr
	}
	return append([]byte(nil), f.loadOrCreateKey...), nil
}

func TestPutGetDeleteSecretRoundTrip(t *testing.T) {
	path := t.TempDir() + "/secrets.enc"
	keys := fakeKeys{
		loadKey:         bytesOf(0x11, 32),
		loadOrCreateKey: bytesOf(0x11, 32),
	}
	store := NewStore(path, keys)
	store.random = bytesOf(0x01, 12)

	if err := store.Put(context.Background(), "prod", Secret{Password: "s3cr3t"}); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	got, err := store.Get(context.Background(), "prod")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Password != "s3cr3t" {
		t.Fatalf("unexpected secret: %#v", got)
	}

	if err := store.Delete(context.Background(), "prod"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := store.Get(context.Background(), "prod"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestGetFailsWhenKeyMissingForExistingFile(t *testing.T) {
	path := t.TempDir() + "/secrets.enc"
	good := fakeKeys{
		loadKey:         bytesOf(0x22, 32),
		loadOrCreateKey: bytesOf(0x22, 32),
	}
	store := NewStore(path, good)
	store.random = bytesOf(0x02, 12)

	if err := store.Put(context.Background(), "prod", Secret{Password: "pw"}); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	bad := NewStore(path, fakeKeys{loadErr: xkeyring.ErrNotFound})
	if _, err := bad.Get(context.Background(), "prod"); !errors.Is(err, xkeyring.ErrNotFound) {
		t.Fatalf("expected keyring not found error, got %v", err)
	}
}

func TestGetFailsWithWrongKey(t *testing.T) {
	path := t.TempDir() + "/secrets.enc"
	store := NewStore(path, fakeKeys{
		loadKey:         bytesOf(0x33, 32),
		loadOrCreateKey: bytesOf(0x33, 32),
	})
	store.random = bytesOf(0x03, 12)

	if err := store.Put(context.Background(), "prod", Secret{Password: "pw"}); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	wrong := NewStore(path, fakeKeys{loadKey: bytesOf(0x44, 32)})
	if _, err := wrong.Get(context.Background(), "prod"); err == nil {
		t.Fatal("expected decryption error with wrong key")
	}
}

func TestGetFailsWithCorruptedFile(t *testing.T) {
	path := t.TempDir() + "/secrets.enc"
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	store := NewStore(path, fakeKeys{loadKey: bytesOf(0x55, 32)})
	if _, err := store.Get(context.Background(), "prod"); err == nil {
		t.Fatal("expected parse error for corrupted file")
	}
}

type byteReader []byte

func bytesOf(b byte, n int) byteReader {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func (r byteReader) Read(p []byte) (int, error) {
	n := copy(p, r)
	return n, nil
}
