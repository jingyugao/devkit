package adapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jingyugao/devkit/internal/xrun/profile"
)

var ErrUnsupportedTool = errors.New("unsupported tool")

type Prepared struct {
	Path    string
	Args    []string
	Env     []string
	Cleanup func() error
}

type Adapter interface {
	ProfileType() profile.Type
	Tools() []string
	DefaultTool() string
	PrepareExec(p profile.Profile, password, binary string, userArgs []string) (Prepared, error)
	PrepareTest(p profile.Profile, password, binary string) (Prepared, error)
}

type Registry struct {
	byTool map[string]Adapter
	byType map[profile.Type]Adapter
}

func NewRegistry() *Registry {
	registry := &Registry{
		byTool: map[string]Adapter{},
		byType: map[profile.Type]Adapter{},
	}
	for _, adapter := range []Adapter{
		mysqlAdapter{},
		mongoAdapter{},
		redisAdapter{},
	} {
		registry.register(adapter)
	}
	return registry
}

func (r *Registry) register(adapter Adapter) {
	r.byType[adapter.ProfileType()] = adapter
	for _, tool := range adapter.Tools() {
		r.byTool[tool] = adapter
	}
}

func (r *Registry) PrepareExec(p profile.Profile, password, binary string, userArgs []string) (Prepared, error) {
	adapter, err := r.adapterForBinary(p, binary)
	if err != nil {
		return Prepared{}, err
	}
	return adapter.PrepareExec(p, password, binary, userArgs)
}

func (r *Registry) PrepareTest(p profile.Profile, password, binary string) (Prepared, error) {
	var adapter Adapter
	var err error
	if binary == "" {
		adapter, err = r.adapterForType(p.Type)
		if err != nil {
			return Prepared{}, err
		}
		binary = adapter.DefaultTool()
	} else {
		adapter, err = r.adapterForBinary(p, binary)
		if err != nil {
			return Prepared{}, err
		}
	}
	return adapter.PrepareTest(p, password, binary)
}

func (r *Registry) DefaultTool(t profile.Type) (string, error) {
	adapter, err := r.adapterForType(t)
	if err != nil {
		return "", err
	}
	return adapter.DefaultTool(), nil
}

func (r *Registry) adapterForType(t profile.Type) (Adapter, error) {
	adapter, ok := r.byType[t]
	if !ok {
		return nil, fmt.Errorf("unsupported profile type %q", t)
	}
	return adapter, nil
}

func (r *Registry) adapterForBinary(p profile.Profile, binary string) (Adapter, error) {
	base := filepath.Base(binary)
	adapter, ok := r.byTool[base]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnsupportedTool, base)
	}
	if adapter.ProfileType() != p.Type {
		return nil, fmt.Errorf("tool %q does not support profile type %q", base, p.Type)
	}
	return adapter, nil
}

func writeTempFile(pattern, contents string) (string, func() error, error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(file.Name())
		return "", nil, err
	}
	if _, err := file.WriteString(contents); err != nil {
		file.Close()
		os.Remove(file.Name())
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		os.Remove(file.Name())
		return "", nil, err
	}
	return file.Name(), func() error { return os.Remove(file.Name()) }, nil
}
