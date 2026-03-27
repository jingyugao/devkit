package importer

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jingyugao/devkit/internal/authrun/profile"
	"github.com/jingyugao/devkit/internal/authrun/store"
)

type SSHInput struct {
	Name           string
	Alias          string
	ConfigPath     string
	KnownHostsPath string
}

type SSHCommandInput struct {
	Name           string
	Target         string
	Args           []string
	KnownHostsPath string
}

type KubeInput struct {
	Name           string
	Context        string
	KubeconfigPath string
}

func ImportSSH(input SSHInput) (profile.Profile, store.Secret, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("profile name is required")
	}
	alias := strings.TrimSpace(input.Alias)
	if alias == "" {
		alias = name
	}

	configPath, err := sshConfigPath(input.ConfigPath)
	if err != nil {
		return profile.Profile{}, store.Secret{}, err
	}
	resolved, err := resolveSSHHost(configPath, alias)
	if err != nil {
		return profile.Profile{}, store.Secret{}, err
	}

	if resolved.user == "" {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("ssh config host %q does not define User", alias)
	}
	if resolved.identityFile == "" {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("ssh config host %q does not define IdentityFile", alias)
	}

	privateKey, err := readRawTextFile(resolved.identityFile)
	if err != nil {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("read ssh private key: %w", err)
	}
	publicKey, _ := readOptionalRawTextFile(resolved.identityFile + ".pub")

	knownHostsPath := strings.TrimSpace(input.KnownHostsPath)
	explicitKnownHosts := knownHostsPath != ""
	if knownHostsPath == "" {
		knownHostsPath = resolved.knownHostsFile
	}
	if knownHostsPath == "" {
		knownHostsPath, err = defaultKnownHostsPath()
		if err != nil {
			return profile.Profile{}, store.Secret{}, err
		}
	}

	var knownHosts string
	switch {
	case explicitKnownHosts:
		knownHosts, err = readRawTextFile(knownHostsPath)
		if err != nil {
			return profile.Profile{}, store.Secret{}, fmt.Errorf("read ssh known_hosts: %w", err)
		}
	case knownHostsPath != "":
		knownHosts, err = readOptionalRawTextFile(knownHostsPath)
		if err != nil {
			return profile.Profile{}, store.Secret{}, fmt.Errorf("read ssh known_hosts: %w", err)
		}
	}

	host := resolved.hostName
	if host == "" {
		host = alias
	}

	p := profile.Profile{
		Name:       name,
		Type:       profile.TypeSSH,
		Host:       host,
		Port:       resolved.port,
		Username:   resolved.user,
		PublicKey:  publicKey,
		KnownHosts: knownHosts,
	}.Normalized()
	if err := p.Validate(); err != nil {
		return profile.Profile{}, store.Secret{}, err
	}

	return p, store.Secret{PrivateKey: privateKey}, nil
}

func ImportSSHCommand(input SSHCommandInput, output CommandOutput) (profile.Profile, store.Secret, error) {
	target := strings.TrimSpace(input.Target)
	if target == "" {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("ssh target is required")
	}
	if output == nil {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("ssh target importer requires a command runner")
	}

	args := append([]string{"-G"}, input.Args...)
	args = append(args, target)
	raw, err := output("ssh", args...)
	if err != nil {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("resolve ssh target %q: %w", target, err)
	}

	resolved, err := parseSSHConfigDump(string(raw))
	if err != nil {
		return profile.Profile{}, store.Secret{}, err
	}
	if resolved.user == "" {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("ssh target %q does not resolve to a user", target)
	}
	if resolved.hostName == "" {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("ssh target %q does not resolve to a hostname", target)
	}

	identityFile, err := selectSSHIdentityFile(resolved.identityFiles)
	if err != nil {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("resolve ssh identity file: %w", err)
	}
	privateKey, err := readRawTextFile(identityFile)
	if err != nil {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("read ssh private key: %w", err)
	}
	publicKey, _ := readOptionalRawTextFile(identityFile + ".pub")

	knownHostsPath := strings.TrimSpace(input.KnownHostsPath)
	var knownHosts string
	if knownHostsPath != "" {
		knownHosts, err = readRawTextFile(knownHostsPath)
		if err != nil {
			return profile.Profile{}, store.Secret{}, fmt.Errorf("read ssh known_hosts: %w", err)
		}
	} else {
		knownHosts, err = readSSHKnownHosts(resolved.knownHostsFiles)
		if err != nil {
			return profile.Profile{}, store.Secret{}, fmt.Errorf("read ssh known_hosts: %w", err)
		}
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = sshCommandProfileName(resolved.user, resolved.hostName)
	}

	p := profile.Profile{
		Name:       name,
		Type:       profile.TypeSSH,
		Host:       resolved.hostName,
		Port:       resolved.port,
		Username:   resolved.user,
		PublicKey:  publicKey,
		KnownHosts: knownHosts,
	}.Normalized()
	if err := p.Validate(); err != nil {
		return profile.Profile{}, store.Secret{}, err
	}

	return p, store.Secret{PrivateKey: privateKey}, nil
}

func ImportKube(input KubeInput) (profile.Profile, store.Secret, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("profile name is required")
	}

	kubeconfigPath, err := defaultKubeconfigPath(input.KubeconfigPath)
	if err != nil {
		return profile.Profile{}, store.Secret{}, err
	}
	cfg, err := loadKubeconfig(kubeconfigPath)
	if err != nil {
		return profile.Profile{}, store.Secret{}, err
	}

	contextName := strings.TrimSpace(input.Context)
	if contextName == "" {
		contextName = strings.TrimSpace(cfg.CurrentContext)
	}
	if contextName == "" {
		return profile.Profile{}, store.Secret{}, fmt.Errorf("kubeconfig does not define a current context")
	}

	ctxEntry, err := cfg.contextByName(contextName)
	if err != nil {
		return profile.Profile{}, store.Secret{}, err
	}
	clusterEntry, err := cfg.clusterByName(ctxEntry.Context.Cluster)
	if err != nil {
		return profile.Profile{}, store.Secret{}, err
	}
	userEntry, err := cfg.userByName(ctxEntry.Context.User)
	if err != nil {
		return profile.Profile{}, store.Secret{}, err
	}

	p := profile.Profile{
		Name:                  name,
		Type:                  profile.TypeKube,
		Server:                strings.TrimSpace(clusterEntry.Cluster.Server),
		Namespace:             strings.TrimSpace(ctxEntry.Context.Namespace),
		Cluster:               strings.TrimSpace(ctxEntry.Context.Cluster),
		Context:               strings.TrimSpace(ctxEntry.Name),
		InsecureSkipTLSVerify: clusterEntry.Cluster.InsecureSkipTLSVerify,
	}
	if clusterEntry.Cluster.CertificateAuthorityData != "" {
		p.CertificateAuthority, err = decodeBase64Data(clusterEntry.Cluster.CertificateAuthorityData, "cluster certificate-authority-data")
		if err != nil {
			return profile.Profile{}, store.Secret{}, err
		}
	} else if clusterEntry.Cluster.CertificateAuthority != "" {
		p.CertificateAuthority, err = readRawTextFile(resolveRelativePath(clusterEntry.Cluster.CertificateAuthority, filepath.Dir(kubeconfigPath)))
		if err != nil {
			return profile.Profile{}, store.Secret{}, fmt.Errorf("read cluster certificate authority: %w", err)
		}
	}

	var secret store.Secret
	switch {
	case strings.TrimSpace(userEntry.User.Token) != "" || strings.TrimSpace(userEntry.User.TokenFile) != "":
		secret.Token, err = kubeToken(userEntry.User, filepath.Dir(kubeconfigPath))
		if err != nil {
			return profile.Profile{}, store.Secret{}, err
		}
	case userEntry.User.Exec != nil:
		execProfile, err := buildKubeExecProfile(userEntry.User.Exec)
		if err != nil {
			return profile.Profile{}, store.Secret{}, err
		}
		p.ExecAPIVersion = execProfile.apiVersion
		p.ExecCommand = execProfile.command
		p.ExecInteractiveMode = execProfile.interactiveMode
		p.ExecArgs = execProfile.args
		p.ExecEnv = execProfile.env
	case userEntry.User.AuthProvider != nil:
		return profile.Profile{}, store.Secret{}, fmt.Errorf("kubeconfig user %q uses unsupported auth-provider %q", userEntry.Name, userEntry.User.AuthProvider.Name)
	default:
		p.ClientCertificate, secret.ClientKey, err = kubeClientCertificate(userEntry.User, filepath.Dir(kubeconfigPath))
		if err != nil {
			return profile.Profile{}, store.Secret{}, err
		}
		if p.ClientCertificate == "" && secret.ClientKey == "" {
			return profile.Profile{}, store.Secret{}, fmt.Errorf("kubeconfig user %q does not contain token, exec, or client certificate auth", userEntry.Name)
		}
	}

	p = p.Normalized()
	if err := p.Validate(); err != nil {
		return profile.Profile{}, store.Secret{}, err
	}
	return p, secret, nil
}

type sshResolved struct {
	hostName        string
	user            string
	port            int
	identityFile    string
	knownHostsFile  string
	identityFiles   []string
	knownHostsFiles []string
}

func resolveSSHHost(configPath, alias string) (sshResolved, error) {
	var resolved sshResolved
	if err := parseSSHConfig(configPath, alias, nil, &resolved, map[string]struct{}{}); err != nil {
		return sshResolved{}, err
	}
	return resolved, nil
}

func parseSSHConfigDump(raw string) (sshResolved, error) {
	resolved := sshResolved{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "hostname":
			resolved.hostName = value
		case "user":
			resolved.user = value
		case "port":
			port, err := strconv.Atoi(value)
			if err != nil {
				return sshResolved{}, fmt.Errorf("parse ssh port: %w", err)
			}
			resolved.port = port
		case "identityfile":
			if value != "" && value != "none" {
				resolved.identityFiles = append(resolved.identityFiles, resolveRelativePath(value, ""))
			}
		case "userknownhostsfile":
			for _, item := range strings.Fields(value) {
				if item == "" || item == "none" {
					continue
				}
				resolved.knownHostsFiles = append(resolved.knownHostsFiles, resolveRelativePath(item, ""))
			}
		}
	}
	if resolved.port == 0 {
		resolved.port = profile.DefaultPort(profile.TypeSSH)
	}
	return resolved, nil
}

func parseSSHConfig(path, alias string, activePatterns []string, resolved *sshResolved, visited map[string]struct{}) error {
	path = resolveRelativePath(path, "")
	if _, seen := visited[path]; seen {
		return nil
	}
	visited[path] = struct{}{}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	configDir := filepath.Dir(path)
	currentPatterns := cloneStrings(activePatterns)
	lines := strings.Split(string(data), "\n")
	for _, rawLine := range lines {
		line := stripSSHComment(rawLine)
		if line == "" {
			continue
		}

		key, value, ok := sshDirective(line)
		if !ok {
			continue
		}

		switch strings.ToLower(key) {
		case "host":
			currentPatterns = strings.Fields(value)
		case "include":
			for _, include := range strings.Fields(value) {
				for _, match := range expandSSHInclude(include, configDir) {
					if err := parseSSHConfig(match, alias, currentPatterns, resolved, visited); err != nil {
						if errors.Is(err, os.ErrNotExist) {
							continue
						}
						return err
					}
				}
			}
		default:
			if !sshPatternsMatch(currentPatterns, alias) {
				continue
			}
			switch strings.ToLower(key) {
			case "hostname":
				if resolved.hostName == "" {
					resolved.hostName = strings.TrimSpace(trimQuotes(value))
				}
			case "user":
				if resolved.user == "" {
					resolved.user = strings.TrimSpace(trimQuotes(value))
				}
			case "port":
				if resolved.port == 0 {
					port, err := strconv.Atoi(strings.TrimSpace(trimQuotes(value)))
					if err != nil {
						return fmt.Errorf("parse ssh port for host %q: %w", alias, err)
					}
					resolved.port = port
				}
			case "identityfile":
				if resolved.identityFile == "" {
					values := strings.Fields(value)
					if len(values) == 0 {
						continue
					}
					resolved.identityFile = resolveRelativePath(values[0], configDir)
				}
			case "userknownhostsfile":
				if resolved.knownHostsFile == "" {
					values := strings.Fields(value)
					if len(values) == 0 {
						continue
					}
					resolved.knownHostsFile = resolveRelativePath(values[0], configDir)
				}
			}
		}
	}
	return nil
}

func sshDirective(line string) (string, string, bool) {
	if idx := strings.IndexAny(line, " \t"); idx >= 0 {
		return line[:idx], strings.TrimSpace(line[idx+1:]), true
	}
	if idx := strings.Index(line, "="); idx >= 0 {
		return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
	}
	return "", "", false
}

func sshPatternsMatch(patterns []string, alias string) bool {
	if len(patterns) == 0 {
		return true
	}
	matched := false
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		negated := strings.HasPrefix(pattern, "!")
		if negated {
			pattern = strings.TrimPrefix(pattern, "!")
		}
		ok, err := filepath.Match(pattern, alias)
		if err != nil || !ok {
			continue
		}
		if negated {
			return false
		}
		matched = true
	}
	return matched
}

func expandSSHInclude(pattern, baseDir string) []string {
	pattern = resolveRelativePath(pattern, baseDir)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return matches
}

func stripSSHComment(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	inQuote := byte(0)
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case inQuote != 0 && c == inQuote:
			inQuote = 0
		case inQuote == 0 && (c == '"' || c == '\''):
			inQuote = c
		case inQuote == 0 && c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return strings.TrimSpace(line[:i])
		}
	}
	return line
}

type kubeconfig struct {
	CurrentContext string          `yaml:"current-context"`
	Clusters       []namedCluster  `yaml:"clusters"`
	Contexts       []namedContext  `yaml:"contexts"`
	Users          []namedAuthInfo `yaml:"users"`
}

type namedCluster struct {
	Name    string      `yaml:"name"`
	Cluster clusterSpec `yaml:"cluster"`
}

type clusterSpec struct {
	Server                   string `yaml:"server"`
	CertificateAuthorityData string `yaml:"certificate-authority-data"`
	CertificateAuthority     string `yaml:"certificate-authority"`
	InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify"`
}

type namedContext struct {
	Name    string      `yaml:"name"`
	Context contextSpec `yaml:"context"`
}

type contextSpec struct {
	Cluster   string `yaml:"cluster"`
	User      string `yaml:"user"`
	Namespace string `yaml:"namespace"`
}

type namedAuthInfo struct {
	Name string       `yaml:"name"`
	User authInfoSpec `yaml:"user"`
}

type authInfoSpec struct {
	Token                 string            `yaml:"token"`
	TokenFile             string            `yaml:"tokenFile"`
	ClientCertificateData string            `yaml:"client-certificate-data"`
	ClientCertificate     string            `yaml:"client-certificate"`
	ClientKeyData         string            `yaml:"client-key-data"`
	ClientKey             string            `yaml:"client-key"`
	Exec                  *execSpec         `yaml:"exec"`
	AuthProvider          *authProviderSpec `yaml:"auth-provider"`
}

type execSpec struct {
	APIVersion      string       `yaml:"apiVersion"`
	Command         string       `yaml:"command"`
	Args            []string     `yaml:"args"`
	Env             []execEnvVar `yaml:"env"`
	InteractiveMode string       `yaml:"interactiveMode"`
}

type execEnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type authProviderSpec struct {
	Name string `yaml:"name"`
}

type kubeExecProfile struct {
	apiVersion      string
	command         string
	args            []string
	env             map[string]string
	interactiveMode string
}

func loadKubeconfig(path string) (kubeconfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return kubeconfig{}, err
	}
	var cfg kubeconfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return kubeconfig{}, fmt.Errorf("parse kubeconfig: %w", err)
	}
	return cfg, nil
}

func (c kubeconfig) contextByName(name string) (namedContext, error) {
	for _, item := range c.Contexts {
		if strings.TrimSpace(item.Name) == name {
			return item, nil
		}
	}
	return namedContext{}, fmt.Errorf("kube context %q not found", name)
}

func (c kubeconfig) clusterByName(name string) (namedCluster, error) {
	for _, item := range c.Clusters {
		if strings.TrimSpace(item.Name) == name {
			return item, nil
		}
	}
	return namedCluster{}, fmt.Errorf("kube cluster %q not found", name)
}

func (c kubeconfig) userByName(name string) (namedAuthInfo, error) {
	for _, item := range c.Users {
		if strings.TrimSpace(item.Name) == name {
			return item, nil
		}
	}
	return namedAuthInfo{}, fmt.Errorf("kube user %q not found", name)
}

func kubeToken(user authInfoSpec, baseDir string) (string, error) {
	if strings.TrimSpace(user.Token) != "" {
		return strings.TrimSpace(user.Token), nil
	}
	if strings.TrimSpace(user.TokenFile) == "" {
		return "", nil
	}
	token, err := readTrimmedTextFile(resolveRelativePath(user.TokenFile, baseDir))
	if err != nil {
		return "", fmt.Errorf("read kube token file: %w", err)
	}
	return token, nil
}

func kubeClientCertificate(user authInfoSpec, baseDir string) (string, string, error) {
	clientCertificate, err := kubeDataOrFile(user.ClientCertificateData, user.ClientCertificate, baseDir, "client certificate")
	if err != nil {
		return "", "", err
	}
	clientKey, err := kubeDataOrFile(user.ClientKeyData, user.ClientKey, baseDir, "client key")
	if err != nil {
		return "", "", err
	}
	if clientCertificate == "" && clientKey == "" {
		return "", "", nil
	}
	if clientCertificate == "" || clientKey == "" {
		return "", "", fmt.Errorf("kube client certificate auth requires both certificate and key")
	}
	return clientCertificate, clientKey, nil
}

func buildKubeExecProfile(spec *execSpec) (kubeExecProfile, error) {
	if spec == nil {
		return kubeExecProfile{}, nil
	}
	if strings.TrimSpace(spec.Command) == "" {
		return kubeExecProfile{}, fmt.Errorf("kube exec auth requires a command")
	}
	env := make(map[string]string, len(spec.Env))
	for _, item := range spec.Env {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		env[name] = strings.TrimSpace(item.Value)
	}
	args := make([]string, 0, len(spec.Args))
	for _, arg := range spec.Args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			args = append(args, arg)
		}
	}
	return kubeExecProfile{
		apiVersion:      strings.TrimSpace(spec.APIVersion),
		command:         strings.TrimSpace(spec.Command),
		args:            args,
		env:             env,
		interactiveMode: strings.TrimSpace(spec.InteractiveMode),
	}, nil
}

func kubeDataOrFile(dataValue, fileValue, baseDir, label string) (string, error) {
	switch {
	case strings.TrimSpace(dataValue) != "" && strings.TrimSpace(fileValue) != "":
		return "", fmt.Errorf("kube %s cannot define both data and file path", label)
	case strings.TrimSpace(dataValue) != "":
		return decodeBase64Data(dataValue, label)
	case strings.TrimSpace(fileValue) != "":
		value, err := readRawTextFile(resolveRelativePath(fileValue, baseDir))
		if err != nil {
			return "", fmt.Errorf("read kube %s: %w", label, err)
		}
		return value, nil
	default:
		return "", nil
	}
}

func decodeBase64Data(value, label string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("decode %s: %w", label, err)
	}
	return strings.TrimRight(string(decoded), "\r\n"), nil
}

func sshConfigPath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		return resolveRelativePath(path, ""), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

func defaultKnownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

func defaultKubeconfigPath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		return resolveRelativePath(path, ""), nil
	}
	if env := os.Getenv("KUBECONFIG"); env != "" {
		for _, item := range filepath.SplitList(env) {
			if strings.TrimSpace(item) != "" {
				return resolveRelativePath(item, ""), nil
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kube", "config"), nil
}

func resolveRelativePath(path, baseDir string) string {
	path = trimQuotes(strings.TrimSpace(path))
	if path == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil {
		path = strings.ReplaceAll(path, "%d", home)
		if path == "~" {
			path = home
		} else if strings.HasPrefix(path, "~/") {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if baseDir == "" {
		baseDir = "."
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func readRawTextFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readOptionalRawTextFile(path string) (string, error) {
	value, err := readRawTextFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return value, err
}

func readTrimmedTextFile(path string) (string, error) {
	value, err := readRawTextFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(value, "\r\n"), nil
}

func readOptionalTrimmedTextFile(path string) (string, error) {
	value, err := readTrimmedTextFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return value, err
}

func selectSSHIdentityFile(paths []string) (string, error) {
	for _, path := range paths {
		path = resolveRelativePath(path, "")
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("no readable identity file found; set IdentityFile in ssh config or pass -i/-oIdentityFile")
}

func readSSHKnownHosts(paths []string) (string, error) {
	seen := map[string]struct{}{}
	var builder strings.Builder

	for _, path := range paths {
		path = resolveRelativePath(path, "")
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}

		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "\n") {
			builder.WriteByte('\n')
		}
		builder.Write(data)
	}

	return builder.String(), nil
}

func sshCommandProfileName(user, host string) string {
	base := strings.TrimSpace(host)
	if strings.TrimSpace(user) != "" {
		base = strings.TrimSpace(user) + "." + base
	}
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.' || r == '_' || r == '-':
			return r
		default:
			return '-'
		}
	}, base)
	base = strings.Trim(base, ".-_")
	if base == "" {
		return "ssh"
	}
	if base[0] >= '0' && base[0] <= '9' {
		return "ssh-" + base
	}
	return base
}

func trimQuotes(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}
