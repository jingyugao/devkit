package profile

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	xrunpaths "github.com/jingyugao/devkit/internal/xrun/paths"
)

var (
	ErrNotFound      = errors.New("profile not found")
	ErrAlreadyExists = errors.New("profile already exists")

	namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

type Type string

const (
	TypeMySQL Type = "mysql"
	TypeMongo Type = "mongo"
	TypeRedis Type = "redis"
)

type Profile struct {
	Name         string `toml:"-"`
	Type         Type   `toml:"type"`
	Host         string `toml:"host,omitempty"`
	Port         int    `toml:"port,omitempty"`
	Username     string `toml:"username,omitempty"`
	Database     string `toml:"database,omitempty"`
	AuthDatabase string `toml:"auth_database,omitempty"`
	TLS          bool   `toml:"tls,omitempty"`
	TLSCAFile    string `toml:"tls_ca_file,omitempty"`
	Socket       string `toml:"socket,omitempty"`
}

type file struct {
	Profiles map[string]Profile `toml:"profiles"`
}

type Store struct {
	path string
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func NewDefaultStore() *Store {
	return NewStore(xrunpaths.ProfilesFile())
}

func DefaultPort(t Type) int {
	switch t {
	case TypeMySQL:
		return 3306
	case TypeMongo:
		return 27017
	case TypeRedis:
		return 6379
	default:
		return 0
	}
}

func (p Profile) Normalized() Profile {
	p.Name = strings.TrimSpace(p.Name)
	p.Type = Type(strings.TrimSpace(string(p.Type)))
	p.Host = strings.TrimSpace(p.Host)
	p.Username = strings.TrimSpace(p.Username)
	p.Database = strings.TrimSpace(p.Database)
	p.AuthDatabase = strings.TrimSpace(p.AuthDatabase)
	p.TLSCAFile = strings.TrimSpace(p.TLSCAFile)
	p.Socket = strings.TrimSpace(p.Socket)
	if p.TLSCAFile != "" {
		p.TLS = true
	}
	if p.Port == 0 {
		p.Port = DefaultPort(p.Type)
	}
	return p
}

func (p Profile) Validate() error {
	p = p.Normalized()

	if !namePattern.MatchString(p.Name) {
		return fmt.Errorf("invalid profile name %q", p.Name)
	}
	if DefaultPort(p.Type) == 0 {
		return fmt.Errorf("unsupported profile type %q", p.Type)
	}
	if p.Port <= 0 {
		return fmt.Errorf("invalid port %d", p.Port)
	}

	switch p.Type {
	case TypeMySQL:
		if p.Host == "" && p.Socket == "" {
			return fmt.Errorf("mysql profile requires --host or --socket")
		}
		if p.Username == "" {
			return fmt.Errorf("mysql profile requires --username")
		}
		if p.AuthDatabase != "" {
			return fmt.Errorf("mysql profile does not support auth_database")
		}
	case TypeMongo:
		if p.Host == "" {
			return fmt.Errorf("mongo profile requires --host")
		}
		if p.Username == "" {
			return fmt.Errorf("mongo profile requires --username")
		}
		if p.Socket != "" {
			return fmt.Errorf("mongo profile does not support socket")
		}
	case TypeRedis:
		if p.Host == "" {
			return fmt.Errorf("redis profile requires --host")
		}
		if p.AuthDatabase != "" {
			return fmt.Errorf("redis profile does not support auth_database")
		}
		if p.Socket != "" {
			return fmt.Errorf("redis profile does not support socket")
		}
	default:
		return fmt.Errorf("unsupported profile type %q", p.Type)
	}

	return nil
}

func (s *Store) Add(p Profile) error {
	p = p.Normalized()
	if err := p.Validate(); err != nil {
		return err
	}

	data, err := s.load()
	if err != nil {
		return err
	}
	if _, exists := data.Profiles[p.Name]; exists {
		return ErrAlreadyExists
	}
	data.Profiles[p.Name] = p
	return s.save(data)
}

func (s *Store) Get(name string) (Profile, error) {
	data, err := s.load()
	if err != nil {
		return Profile{}, err
	}
	name = strings.TrimSpace(name)
	p, ok := data.Profiles[name]
	if !ok {
		return Profile{}, ErrNotFound
	}
	p.Name = name
	return p, nil
}

func (s *Store) List() ([]Profile, error) {
	data, err := s.load()
	if err != nil {
		return nil, err
	}

	profiles := make([]Profile, 0, len(data.Profiles))
	for name, p := range data.Profiles {
		p.Name = name
		profiles = append(profiles, p.Normalized())
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})
	return profiles, nil
}

func (s *Store) Delete(name string) error {
	data, err := s.load()
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if _, exists := data.Profiles[name]; !exists {
		return ErrNotFound
	}
	delete(data.Profiles, name)
	return s.save(data)
}

func (s *Store) load() (file, error) {
	data := file{Profiles: map[string]Profile{}}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return data, nil
		}
		return file{}, err
	}
	if len(raw) == 0 {
		return data, nil
	}
	if err := toml.Unmarshal(raw, &data); err != nil {
		return file{}, fmt.Errorf("parse profiles: %w", err)
	}
	if data.Profiles == nil {
		data.Profiles = map[string]Profile{}
	}
	for name, p := range data.Profiles {
		p.Name = name
		data.Profiles[name] = p.Normalized()
	}
	return data, nil
}

func (s *Store) save(data file) error {
	if data.Profiles == nil {
		data.Profiles = map[string]Profile{}
	}
	if err := xrunpaths.EnsureBaseDir(); err != nil {
		return err
	}
	raw, err := toml.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal profiles: %w", err)
	}
	return atomicWrite(s.path, raw, 0o600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
