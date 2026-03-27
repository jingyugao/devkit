package importer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jingyugao/devkit/internal/authrun/profile"
	"github.com/jingyugao/devkit/internal/authrun/store"
)

type CommandOutput func(string, ...string) ([]byte, error)

type MySQLInput struct {
	Name      string
	LoginPath string
	Database  string
}

func ImportMySQL(input MySQLInput, output CommandOutput) (profile.Profile, store.Secret, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("profile name is required")
	}
	loginPath := strings.TrimSpace(input.LoginPath)
	if loginPath == "" {
		loginPath = name
	}
	if output == nil {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("mysql login path importer requires a command runner")
	}

	raw, err := output("mysql_config_editor", "print", "--login-path="+loginPath)
	if err != nil {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("read mysql login path %q: %w", loginPath, err)
	}

	p := profile.Profile{
		Name:           name,
		Type:           profile.TypeMySQL,
		MySQLLoginPath: loginPath,
		Database:       strings.TrimSpace(input.Database),
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = trimQuotes(strings.TrimSpace(value))
		switch key {
		case "user":
			p.Username = value
		case "host":
			p.Host = value
		case "port":
			port, err := strconv.Atoi(value)
			if err != nil {
				return profile.Profile{}, store.Secret{}, fmt.Errorf("parse mysql login path port: %w", err)
			}
			p.Port = port
		case "socket":
			p.Socket = value
		}
	}

	p = p.Normalized()
	if err := p.Validate(); err != nil {
		return profile.Profile{}, store.Secret{}, err
	}
	return p, store.Secret{}, nil
}
