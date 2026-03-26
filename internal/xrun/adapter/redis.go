package adapter

import (
	"strconv"

	"github.com/jingyugao/devkit/internal/xrun/profile"
)

type redisAdapter struct{}

func (redisAdapter) ProfileType() profile.Type {
	return profile.TypeRedis
}

func (redisAdapter) Tools() []string {
	return []string{"redis-cli"}
}

func (redisAdapter) DefaultTool() string {
	return "redis-cli"
}

func (redisAdapter) PrepareExec(p profile.Profile, password, binary string, userArgs []string) (Prepared, error) {
	return prepareRedis(p, password, binary, userArgs)
}

func (redisAdapter) PrepareTest(p profile.Profile, password, binary string) (Prepared, error) {
	return prepareRedis(p, password, binary, []string{"PING"})
}

func prepareRedis(p profile.Profile, password, binary string, userArgs []string) (Prepared, error) {
	args := []string{"-h", p.Host, "-p", strconv.Itoa(p.Port)}
	if p.Database != "" {
		args = append(args, "-n", p.Database)
	}
	if p.Username != "" {
		args = append(args, "--user", p.Username)
	}
	if p.TLS {
		args = append(args, "--tls")
	}
	if p.TLSCAFile != "" {
		args = append(args, "--cacert", p.TLSCAFile)
	}
	args = append(args, userArgs...)

	return Prepared{
		Path: binary,
		Args: args,
		Env:  []string{"REDISCLI_AUTH=" + password},
	}, nil
}
