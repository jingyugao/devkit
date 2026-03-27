package adapter

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jingyugao/devkit/internal/authrun/profile"
	"github.com/jingyugao/devkit/internal/authrun/store"
)

type mongoAdapter struct{}

func (mongoAdapter) ProfileType() profile.Type {
	return profile.TypeMongo
}

func (mongoAdapter) Tools() []string {
	return []string{"mongosh"}
}

func (mongoAdapter) DefaultTool() string {
	return "mongosh"
}

func (mongoAdapter) PrepareExec(p profile.Profile, secret store.Secret, binary string, userArgs []string) (Prepared, error) {
	script, cleanup, err := mongoScript(p, secret.Password, true)
	if err != nil {
		return Prepared{}, err
	}
	args := mongoBaseArgs(p)
	args = append(args, userArgs...)
	args = append(args, "--file", script, "--shell")
	return Prepared{
		Path:    binary,
		Args:    args,
		Cleanup: cleanup,
	}, nil
}

func (mongoAdapter) PrepareTest(p profile.Profile, secret store.Secret, binary string) (Prepared, error) {
	script, cleanup, err := mongoScript(p, secret.Password, false)
	if err != nil {
		return Prepared{}, err
	}
	args := append(mongoBaseArgs(p), "--quiet", "--norc", "--file", script)
	return Prepared{
		Path:    binary,
		Args:    args,
		Cleanup: cleanup,
	}, nil
}

func mongoBaseArgs(p profile.Profile) []string {
	args := []string{"--host", p.Host, "--port", strconv.Itoa(p.Port)}
	if p.TLS {
		args = append(args, "--tls")
	}
	if p.TLSCAFile != "" {
		args = append(args, "--tlsCAFile", p.TLSCAFile)
	}
	return args
}

func mongoScript(p profile.Profile, password string, interactive bool) (string, func() error, error) {
	authDB := p.AuthDatabase
	if authDB == "" {
		authDB = "admin"
	}
	targetDB := p.Database
	if targetDB == "" {
		targetDB = authDB
	}

	script := []string{
		fmt.Sprintf("db = db.getSiblingDB(%s);", mustJSONString(authDB)),
		fmt.Sprintf("if (!db.auth(%s, %s)) { quit(1); }", mustJSONString(p.Username), mustJSONString(password)),
		fmt.Sprintf("db = db.getMongo().getDB(%s);", mustJSONString(targetDB)),
	}
	if interactive {
		script = append(script, "print('authrun authentication loaded');")
	} else {
		script = append(script,
			"const result = db.runCommand({ ping: 1 });",
			"if (!result || result.ok !== 1) { quit(1); }",
			"quit(0);",
		)
	}
	path, cleanup, err := writeTempFile("authrun-mongo-*.js", strings.Join(script, "\n")+"\n")
	if err != nil {
		return "", nil, fmt.Errorf("create mongo bootstrap script: %w", err)
	}
	return path, cleanup, nil
}

func mustJSONString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
