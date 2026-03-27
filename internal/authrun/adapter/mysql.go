package adapter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jingyugao/devkit/internal/authrun/profile"
	"github.com/jingyugao/devkit/internal/authrun/store"
)

type mysqlAdapter struct{}

func (mysqlAdapter) ProfileType() profile.Type {
	return profile.TypeMySQL
}

func (mysqlAdapter) Tools() []string {
	return []string{"mysql"}
}

func (mysqlAdapter) DefaultTool() string {
	return "mysql"
}

func (mysqlAdapter) PrepareExec(p profile.Profile, secret store.Secret, binary string, userArgs []string) (Prepared, error) {
	return prepareMySQL(p, secret.Password, binary, userArgs)
}

func (mysqlAdapter) PrepareTest(p profile.Profile, secret store.Secret, binary string) (Prepared, error) {
	return prepareMySQL(p, secret.Password, binary, []string{"-e", "SELECT 1"})
}

func prepareMySQL(p profile.Profile, password, binary string, userArgs []string) (Prepared, error) {
	if p.MySQLLoginPath != "" {
		args := []string{"--login-path=" + p.MySQLLoginPath}
		if p.Username != "" {
			args = append(args, "--user="+p.Username)
		}
		if p.Host != "" {
			args = append(args, "--host="+p.Host)
		}
		if p.Port > 0 {
			args = append(args, "--port="+strconv.Itoa(p.Port))
		}
		if p.Socket != "" {
			args = append(args, "--socket="+p.Socket)
		}
		if p.TLS {
			args = append(args, "--ssl-mode=REQUIRED")
		}
		if p.TLSCAFile != "" {
			args = append(args, "--ssl-ca="+p.TLSCAFile)
		}
		if p.Database != "" {
			args = append(args, "--database="+p.Database)
		}
		args = append(args, userArgs...)
		return Prepared{
			Path: binary,
			Args: args,
		}, nil
	}

	if password == "" {
		return Prepared{}, fmt.Errorf("mysql profile %q requires a password secret or mysql login path", p.Name)
	}

	config := []string{"[client]"}
	if p.Username != "" {
		config = append(config, "user="+p.Username)
	}
	config = append(config, "password="+password)
	if p.Host != "" {
		config = append(config, "host="+p.Host)
	}
	if p.Port > 0 {
		config = append(config, "port="+strconv.Itoa(p.Port))
	}
	if p.Socket != "" {
		config = append(config, "socket="+p.Socket)
	}
	if p.TLS {
		config = append(config, "ssl-mode=REQUIRED")
	}
	if p.TLSCAFile != "" {
		config = append(config, "ssl-ca="+p.TLSCAFile)
	}

	path, cleanup, err := writeTempFile("authrun-mysql-*.cnf", strings.Join(config, "\n")+"\n")
	if err != nil {
		return Prepared{}, fmt.Errorf("create mysql option file: %w", err)
	}

	args := []string{"--defaults-extra-file=" + path}
	if p.Database != "" {
		args = append(args, "--database="+p.Database)
	}
	args = append(args, userArgs...)
	return Prepared{
		Path:    binary,
		Args:    args,
		Cleanup: cleanup,
	}, nil
}
