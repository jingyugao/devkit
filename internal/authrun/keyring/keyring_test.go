package keyring

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
)

type fakeBackend struct {
	values map[string]string
	errGet error
}

func (b *fakeBackend) Get(service, user string) (string, error) {
	if b.errGet != nil {
		return "", b.errGet
	}
	value, ok := b.values[service+":"+user]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func (b *fakeBackend) Set(service, user, password string) error {
	if b.values == nil {
		b.values = map[string]string{}
	}
	b.values[service+":"+user] = password
	return nil
}

func (b *fakeBackend) Delete(service, user string) error {
	delete(b.values, service+":"+user)
	return nil
}

func TestLoadOrCreateCreatesAndReusesMasterKey(t *testing.T) {
	backend := &fakeBackend{}
	manager := NewManagerWithBackend(backend)
	manager.random = bytesOf(0x42, 32)

	first, err := manager.LoadOrCreate(context.Background())
	if err != nil {
		t.Fatalf("LoadOrCreate returned error: %v", err)
	}
	second, err := manager.LoadOrCreate(context.Background())
	if err != nil {
		t.Fatalf("second LoadOrCreate returned error: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("expected same stored key")
	}
}

func TestLoadReturnsNotFound(t *testing.T) {
	manager := NewManagerWithBackend(&fakeBackend{})

	if _, err := manager.Load(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestLoadRejectsInvalidStoredKey(t *testing.T) {
	backend := &fakeBackend{values: map[string]string{
		defaultService + ":" + defaultUser: base64.StdEncoding.EncodeToString([]byte("short")),
	}}
	manager := NewManagerWithBackend(backend)

	if _, err := manager.Load(context.Background()); err == nil {
		t.Fatal("expected invalid key error")
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
