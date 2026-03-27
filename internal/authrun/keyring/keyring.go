package keyring

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	oskeyring "github.com/zalando/go-keyring"
)

const (
	defaultService = "authrun"
	defaultUser    = "master-key"
	keySize        = 32
)

var ErrNotFound = errors.New("keyring entry not found")

type Backend interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

type Manager struct {
	backend Backend
	random  io.Reader
	service string
	user    string
}

type osBackend struct{}

func NewManager() *Manager {
	return &Manager{
		backend: osBackend{},
		random:  rand.Reader,
		service: defaultService,
		user:    defaultUser,
	}
}

func NewManagerWithBackend(backend Backend) *Manager {
	manager := NewManager()
	manager.backend = backend
	return manager
}

func (m *Manager) Load(_ context.Context) ([]byte, error) {
	value, err := m.backend.Get(m.service, m.user)
	if err != nil {
		return nil, err
	}
	return decodeKey(value)
}

func (m *Manager) LoadOrCreate(ctx context.Context) ([]byte, error) {
	key, err := m.Load(ctx)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	key = make([]byte, keySize)
	if _, err := io.ReadFull(m.random, key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	if err := m.backend.Set(m.service, m.user, base64.StdEncoding.EncodeToString(key)); err != nil {
		return nil, fmt.Errorf("store master key: %w", err)
	}
	return key, nil
}

func (m *Manager) Delete(_ context.Context) error {
	if err := m.backend.Delete(m.service, m.user); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

func decodeKey(value string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode master key: %w", err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("invalid master key length %d", len(key))
	}
	return key, nil
}

func (osBackend) Get(service, user string) (string, error) {
	value, err := oskeyring.Get(service, user)
	if errors.Is(err, oskeyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return value, err
}

func (osBackend) Set(service, user, password string) error {
	return oskeyring.Set(service, user, password)
}

func (osBackend) Delete(service, user string) error {
	err := oskeyring.Delete(service, user)
	if errors.Is(err, oskeyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
