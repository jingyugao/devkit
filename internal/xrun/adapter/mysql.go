package adapter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jingyugao/devkit/internal/xrun/profile"
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

func (mysqlAdapter) PrepareExec(p profile.Profile, password, binary string, userArgs []string) (Prepared, error) {
	return prepareMySQL(p, password, binary, userArgs)
}

func (mysqlAdapter) PrepareTest(p profile.Profile, password, binary string) (Prepared, error) {
	return prepareMySQL(p, password, binary, []string{"-e", "SELECT 1"})
}

func prepareMySQL(p profile.Profile, password, binary string, userArgs []string) (Prepared, error) {
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

	path, cleanup, err := writeTempFile("xrun-mysql-*.cnf", strings.Join(config, "\n")+"\n")
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
