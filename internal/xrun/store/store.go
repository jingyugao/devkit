package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	xrunpaths "github.com/jingyugao/devkit/internal/xrun/paths"
)

const currentVersion = 1

var ErrNotFound = errors.New("secret not found")

type MasterKeyProvider interface {
	Load(ctx context.Context) ([]byte, error)
	LoadOrCreate(ctx context.Context) ([]byte, error)
}

type Secret struct {
	Password string `json:"password"`
}

type envelope struct {
	Version    int    `json:"version"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type payload struct {
	Profiles map[string]Secret `json:"profiles"`
}

type Store struct {
	path   string
	keys   MasterKeyProvider
	random io.Reader
}

func NewStore(path string, keys MasterKeyProvider) *Store {
	return &Store{
		path:   path,
		keys:   keys,
		random: rand.Reader,
	}
}

func NewDefaultStore(keys MasterKeyProvider) *Store {
	return NewStore(xrunpaths.SecretsFile(), keys)
}

func (s *Store) Put(ctx context.Context, name string, secret Secret) error {
	data, err := s.load(ctx)
	if err != nil {
		return err
	}
	if data.Profiles == nil {
		data.Profiles = map[string]Secret{}
	}
	data.Profiles[name] = secret
	return s.save(ctx, data)
}

func (s *Store) Get(ctx context.Context, name string) (Secret, error) {
	data, err := s.load(ctx)
	if err != nil {
		return Secret{}, err
	}
	secret, ok := data.Profiles[name]
	if !ok {
		return Secret{}, ErrNotFound
	}
	return secret, nil
}

func (s *Store) Delete(ctx context.Context, name string) error {
	data, err := s.load(ctx)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if len(data.Profiles) == 0 {
		return nil
	}
	delete(data.Profiles, name)
	return s.save(ctx, data)
}

func (s *Store) load(ctx context.Context) (payload, error) {
	data := payload{Profiles: map[string]Secret{}}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return data, nil
		}
		return payload{}, err
	}
	if len(raw) == 0 {
		return data, nil
	}

	key, err := s.keys.Load(ctx)
	if err != nil {
		return payload{}, err
	}
	plain, err := decrypt(key, raw)
	if err != nil {
		return payload{}, err
	}
	if err := json.Unmarshal(plain, &data); err != nil {
		return payload{}, fmt.Errorf("parse secrets payload: %w", err)
	}
	if data.Profiles == nil {
		data.Profiles = map[string]Secret{}
	}
	return data, nil
}

func (s *Store) save(ctx context.Context, data payload) error {
	if data.Profiles == nil {
		data.Profiles = map[string]Secret{}
	}
	if err := xrunpaths.EnsureBaseDir(); err != nil {
		return err
	}
	key, err := s.keys.LoadOrCreate(ctx)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal secrets payload: %w", err)
	}
	enc, err := s.encrypt(key, plain)
	if err != nil {
		return err
	}
	return atomicWrite(s.path, enc, 0o600)
}

func (s *Store) encrypt(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(s.random, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	data := envelope{
		Version:    currentVersion,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, plain, nil)),
	}
	return json.Marshal(data)
}

func decrypt(key, raw []byte) ([]byte, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse secrets envelope: %w", err)
	}
	if env.Version != currentVersion {
		return nil, fmt.Errorf("unsupported secrets version %d", env.Version)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt secrets: %w", err)
	}
	return plain, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
