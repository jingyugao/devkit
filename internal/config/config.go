package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/jingyugao/keep-run/internal/paths"
)

type Defaults struct {
	Life        string   `toml:"life,omitempty"`
	StopTimeout string   `toml:"stop_timeout,omitempty"`
	EnvPass     []string `toml:"env_pass,omitempty"`
}

type Logs struct {
	TailLines int `toml:"tail_lines,omitempty"`
}

type Config struct {
	Defaults Defaults `toml:"defaults"`
	Logs     Logs     `toml:"logs"`
}

func Builtins() Config {
	return Config{
		Defaults: Defaults{
			Life:        "",
			StopTimeout: "10s",
			EnvPass:     []string{},
		},
		Logs: Logs{
			TailLines: 200,
		},
	}
}

func Load() (Config, error) {
	cfg := Builtins()
	data, err := os.ReadFile(paths.ConfigFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Defaults.StopTimeout == "" {
		cfg.Defaults.StopTimeout = Builtins().Defaults.StopTimeout
	}
	if cfg.Logs.TailLines <= 0 {
		cfg.Logs.TailLines = Builtins().Logs.TailLines
	}
	if cfg.Defaults.EnvPass == nil {
		cfg.Defaults.EnvPass = []string{}
	}
	return cfg, nil
}

func Save(cfg Config) error {
	if err := paths.EnsureBaseDirs(); err != nil {
		return err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return atomicWrite(paths.ConfigFile(), data, 0o644)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func Keys() []string {
	keys := []string{
		"defaults.env_pass",
		"defaults.life",
		"defaults.stop_timeout",
		"logs.tail_lines",
	}
	sort.Strings(keys)
	return keys
}

func Get(cfg Config, key string) (string, error) {
	switch key {
	case "defaults.life":
		return cfg.Defaults.Life, nil
	case "defaults.stop_timeout":
		return cfg.Defaults.StopTimeout, nil
	case "defaults.env_pass":
		return strings.Join(cfg.Defaults.EnvPass, ","), nil
	case "logs.tail_lines":
		return fmt.Sprintf("%d", cfg.Logs.TailLines), nil
	default:
		return "", fmt.Errorf("unknown config key %q", key)
	}
}

func Set(cfg *Config, key string, value string) error {
	switch key {
	case "defaults.life":
		cfg.Defaults.Life = strings.TrimSpace(value)
	case "defaults.stop_timeout":
		cfg.Defaults.StopTimeout = strings.TrimSpace(value)
	case "defaults.env_pass":
		if strings.TrimSpace(value) == "" {
			cfg.Defaults.EnvPass = []string{}
			return nil
		}
		parts := strings.Split(value, ",")
		result := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			result = append(result, part)
		}
		cfg.Defaults.EnvPass = result
	case "logs.tail_lines":
		var lines int
		if _, err := fmt.Sscanf(value, "%d", &lines); err != nil || lines <= 0 {
			return fmt.Errorf("invalid positive integer %q", value)
		}
		cfg.Logs.TailLines = lines
	default:
		return fmt.Errorf("unknown config key %q", key)
	}
	return nil
}

func Unset(cfg *Config, key string) error {
	builtins := Builtins()
	switch key {
	case "defaults.life":
		cfg.Defaults.Life = builtins.Defaults.Life
	case "defaults.stop_timeout":
		cfg.Defaults.StopTimeout = builtins.Defaults.StopTimeout
	case "defaults.env_pass":
		cfg.Defaults.EnvPass = builtins.Defaults.EnvPass
	case "logs.tail_lines":
		cfg.Logs.TailLines = builtins.Logs.TailLines
	default:
		return fmt.Errorf("unknown config key %q", key)
	}
	return nil
}
