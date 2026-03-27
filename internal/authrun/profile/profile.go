package profile

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	authrunpaths "github.com/jingyugao/devkit/internal/authrun/paths"
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
	TypeSSH   Type = "ssh"
	TypeKube  Type = "kube"
)

type Profile struct {
	Name                  string            `toml:"-"`
	Type                  Type              `toml:"type"`
	Host                  string            `toml:"host,omitempty"`
	Port                  int               `toml:"port,omitempty"`
	Username              string            `toml:"username,omitempty"`
	Database              string            `toml:"database,omitempty"`
	AuthDatabase          string            `toml:"auth_database,omitempty"`
	MySQLLoginPath        string            `toml:"mysql_login_path,omitempty"`
	Namespace             string            `toml:"namespace,omitempty"`
	Cluster               string            `toml:"cluster,omitempty"`
	Context               string            `toml:"context,omitempty"`
	Server                string            `toml:"server,omitempty"`
	TLS                   bool              `toml:"tls,omitempty"`
	TLSCAFile             string            `toml:"tls_ca_file,omitempty"`
	CertificateAuthority  string            `toml:"certificate_authority,omitempty"`
	ClientCertificate     string            `toml:"client_certificate,omitempty"`
	ExecAPIVersion        string            `toml:"exec_api_version,omitempty"`
	ExecCommand           string            `toml:"exec_command,omitempty"`
	ExecInteractiveMode   string            `toml:"exec_interactive_mode,omitempty"`
	ExecArgs              []string          `toml:"exec_args,omitempty"`
	ExecEnv               map[string]string `toml:"exec_env,omitempty"`
	InsecureSkipTLSVerify bool              `toml:"insecure_skip_tls_verify,omitempty"`
	Socket                string            `toml:"socket,omitempty"`
	PublicKey             string            `toml:"public_key,omitempty"`
	KnownHosts            string            `toml:"known_hosts,omitempty"`
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
	return NewStore(authrunpaths.ProfilesFile())
}

func DefaultPort(t Type) int {
	switch t {
	case TypeMySQL:
		return 3306
	case TypeMongo:
		return 27017
	case TypeRedis:
		return 6379
	case TypeSSH:
		return 22
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
	p.MySQLLoginPath = strings.TrimSpace(p.MySQLLoginPath)
	p.Namespace = strings.TrimSpace(p.Namespace)
	p.Cluster = strings.TrimSpace(p.Cluster)
	p.Context = strings.TrimSpace(p.Context)
	p.Server = strings.TrimSpace(p.Server)
	p.TLSCAFile = strings.TrimSpace(p.TLSCAFile)
	p.CertificateAuthority = strings.TrimSpace(p.CertificateAuthority)
	p.ClientCertificate = strings.TrimSpace(p.ClientCertificate)
	p.ExecAPIVersion = strings.TrimSpace(p.ExecAPIVersion)
	p.ExecCommand = strings.TrimSpace(p.ExecCommand)
	p.ExecInteractiveMode = strings.TrimSpace(p.ExecInteractiveMode)
	if len(p.ExecArgs) > 0 {
		args := make([]string, 0, len(p.ExecArgs))
		for _, arg := range p.ExecArgs {
			arg = strings.TrimSpace(arg)
			if arg != "" {
				args = append(args, arg)
			}
		}
		p.ExecArgs = args
	}
	if len(p.ExecEnv) > 0 {
		env := make(map[string]string, len(p.ExecEnv))
		for key, value := range p.ExecEnv {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			env[key] = strings.TrimSpace(value)
		}
		p.ExecEnv = env
	}
	p.Socket = strings.TrimSpace(p.Socket)
	p.PublicKey = strings.TrimSpace(p.PublicKey)
	p.KnownHosts = strings.TrimSpace(p.KnownHosts)
	if p.TLSCAFile != "" {
		p.TLS = true
	}
	if p.CertificateAuthority != "" || p.ClientCertificate != "" {
		p.TLS = true
	}
	if p.Port == 0 && DefaultPort(p.Type) != 0 {
		p.Port = DefaultPort(p.Type)
	}
	if p.Type == TypeKube {
		if p.Cluster == "" {
			p.Cluster = p.Name
		}
		if p.Context == "" {
			p.Context = p.Name
		}
	}
	return p
}

func (p Profile) Validate() error {
	p = p.Normalized()

	if !namePattern.MatchString(p.Name) {
		return fmt.Errorf("invalid profile name %q", p.Name)
	}
	if p.Type != TypeKube && DefaultPort(p.Type) == 0 {
		return fmt.Errorf("unsupported profile type %q", p.Type)
	}
	if p.Type != TypeKube && p.Port <= 0 {
		return fmt.Errorf("invalid port %d", p.Port)
	}

	switch p.Type {
	case TypeMySQL:
		if p.Host == "" && p.Socket == "" && p.MySQLLoginPath == "" {
			return fmt.Errorf("mysql profile requires --host, --socket, or mysql_login_path")
		}
		if p.Username == "" && p.MySQLLoginPath == "" {
			return fmt.Errorf("mysql profile requires --username unless mysql_login_path is set")
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
		if p.MySQLLoginPath != "" {
			return fmt.Errorf("mongo profile does not support mysql login paths")
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
		if p.MySQLLoginPath != "" {
			return fmt.Errorf("redis profile does not support mysql login paths")
		}
		if p.Socket != "" {
			return fmt.Errorf("redis profile does not support socket")
		}
	case TypeSSH:
		if p.Host == "" {
			return fmt.Errorf("ssh profile requires --host")
		}
		if p.Username == "" {
			return fmt.Errorf("ssh profile requires --username")
		}
		if p.Database != "" || p.AuthDatabase != "" || p.MySQLLoginPath != "" || p.Namespace != "" || p.Server != "" || p.Cluster != "" || p.Context != "" {
			return fmt.Errorf("ssh profile contains unsupported database or kube fields")
		}
		if p.CertificateAuthority != "" || p.ClientCertificate != "" || p.ExecCommand != "" || p.ExecAPIVersion != "" || p.ExecInteractiveMode != "" || len(p.ExecArgs) != 0 || len(p.ExecEnv) != 0 {
			return fmt.Errorf("ssh profile does not support kube certificate fields")
		}
		if p.Socket != "" {
			return fmt.Errorf("ssh profile does not support socket")
		}
	case TypeKube:
		if p.Server == "" {
			return fmt.Errorf("kube profile requires --server")
		}
		if p.Host != "" || p.Port != 0 || p.Username != "" || p.Database != "" || p.AuthDatabase != "" || p.MySQLLoginPath != "" || p.Socket != "" {
			return fmt.Errorf("kube profile contains unsupported host or database fields")
		}
		if p.PublicKey != "" || p.KnownHosts != "" {
			return fmt.Errorf("kube profile does not support ssh key fields")
		}
		if p.InsecureSkipTLSVerify && p.CertificateAuthority != "" {
			return fmt.Errorf("kube profile cannot use certificate authority data with --insecure-skip-tls-verify")
		}
		if p.ExecCommand == "" && (p.ExecAPIVersion != "" || p.ExecInteractiveMode != "" || len(p.ExecArgs) != 0 || len(p.ExecEnv) != 0) {
			return fmt.Errorf("kube profile exec settings require exec_command")
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
	if err := authrunpaths.EnsureBaseDir(); err != nil {
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
