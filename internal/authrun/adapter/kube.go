package adapter

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jingyugao/devkit/internal/authrun/profile"
	"github.com/jingyugao/devkit/internal/authrun/store"
)

type kubeAdapter struct{}

func (kubeAdapter) ProfileType() profile.Type {
	return profile.TypeKube
}

func (kubeAdapter) Tools() []string {
	return []string{"kubectl", "k9s"}
}

func (kubeAdapter) DefaultTool() string {
	return "kubectl"
}

func (kubeAdapter) PrepareExec(p profile.Profile, secret store.Secret, binary string, userArgs []string) (Prepared, error) {
	return prepareKubeAggregate([]kubeProfileSecret{{profile: p, secret: secret}}, binary, userArgs)
}

func (kubeAdapter) PrepareTest(p profile.Profile, secret store.Secret, binary string) (Prepared, error) {
	tool := filepath.Base(binary)
	if tool == "k9s" {
		return Prepared{}, fmt.Errorf("authrun test does not support k9s; use kubectl or omit --tool")
	}

	kubeconfigPath, cleanup, err := writeKubeconfig(p, secret)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{
		Path:    "kubectl",
		Args:    []string{"version", "--output=json"},
		Env:     []string{"KUBECONFIG=" + kubeconfigPath},
		Cleanup: cleanup,
	}, nil
}

func writeKubeconfig(p profile.Profile, secret store.Secret) (string, func() error, error) {
	return writeCombinedKubeconfig([]kubeProfileSecret{{profile: p, secret: secret}})
}

func prepareKubeAggregate(entries []kubeProfileSecret, binary string, userArgs []string) (Prepared, error) {
	kubeconfigPath, cleanup, err := writeCombinedKubeconfig(entries)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{
		Path:    binary,
		Args:    append([]string{}, userArgs...),
		Env:     []string{"KUBECONFIG=" + kubeconfigPath},
		Cleanup: cleanup,
	}, nil
}

func writeCombinedKubeconfig(entries []kubeProfileSecret) (string, func() error, error) {
	if len(entries) == 0 {
		return "", nil, fmt.Errorf("no kube profiles provided")
	}

	clusters := make([]string, 0, len(entries))
	contexts := make([]string, 0, len(entries))
	users := make([]string, 0, len(entries))
	usedClusters := map[string]struct{}{}
	usedContexts := map[string]struct{}{}
	usedUsers := map[string]struct{}{}
	currentContext := ""

	for _, entry := range entries {
		clusterName := uniqueKubeName(entry.profile.Cluster, entry.profile.Name+"-cluster", usedClusters)
		contextName := uniqueKubeName(entry.profile.Context, entry.profile.Name, usedContexts)
		userName := uniqueKubeName(entry.profile.Name, entry.profile.Name+"-user", usedUsers)

		clusterLines := []string{
			"    server: " + yamlScalar(entry.profile.Server),
		}
		if entry.profile.InsecureSkipTLSVerify {
			clusterLines = append(clusterLines, "    insecure-skip-tls-verify: true")
		} else if entry.profile.CertificateAuthority != "" {
			clusterLines = append(clusterLines, "    certificate-authority-data: "+yamlScalar(base64.StdEncoding.EncodeToString([]byte(entry.profile.CertificateAuthority))))
		}

		userSection, err := kubeUserSection(entry.profile, entry.secret)
		if err != nil {
			return "", nil, err
		}

		contextLines := []string{
			"    cluster: " + yamlScalar(clusterName),
			"    user: " + yamlScalar(userName),
		}
		if entry.profile.Namespace != "" {
			contextLines = append(contextLines, "    namespace: "+yamlScalar(entry.profile.Namespace))
		}

		clusters = append(clusters, fmt.Sprintf(`- name: %s
  cluster:
%s`, yamlScalar(clusterName), strings.Join(clusterLines, "\n")))
		contexts = append(contexts, fmt.Sprintf(`- name: %s
  context:
%s`, yamlScalar(contextName), strings.Join(contextLines, "\n")))
		users = append(users, fmt.Sprintf(`- name: %s
  user:
%s`, yamlScalar(userName), userSection))
		if currentContext == "" {
			currentContext = contextName
		}
	}

	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
%s
contexts:
%s
current-context: %s
users:
%s
`,
		strings.Join(clusters, "\n"),
		strings.Join(contexts, "\n"),
		yamlScalar(currentContext),
		strings.Join(users, "\n"))

	path, cleanup, err := writeTempFile("authrun-kubeconfig-*.yaml", kubeconfig)
	if err != nil {
		return "", nil, fmt.Errorf("create kubeconfig: %w", err)
	}
	return path, cleanup, nil
}

func kubeUserSection(p profile.Profile, secret store.Secret) (string, error) {
	switch {
	case secret.Token != "" && (secret.ClientKey != "" || p.ClientCertificate != "" || p.ExecCommand != ""):
		return "", fmt.Errorf("kube profile %q cannot mix token auth with client certificate auth", p.Name)
	case secret.Token != "":
		return "    token: " + yamlScalar(secret.Token), nil
	case p.ExecCommand != "" && (secret.ClientKey != "" || p.ClientCertificate != ""):
		return "", fmt.Errorf("kube profile %q cannot mix exec auth with client certificate auth", p.Name)
	case p.ExecCommand != "":
		lines := []string{
			"    exec:",
			"      apiVersion: " + yamlScalar(kubeExecAPIVersion(p)),
			"      command: " + yamlScalar(p.ExecCommand),
		}
		if len(p.ExecArgs) > 0 {
			lines = append(lines, "      args:")
			for _, arg := range p.ExecArgs {
				lines = append(lines, "      - "+yamlScalar(arg))
			}
		}
		if len(p.ExecEnv) > 0 {
			lines = append(lines, "      env:")
			keys := make([]string, 0, len(p.ExecEnv))
			for key := range p.ExecEnv {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				lines = append(lines,
					"      - name: "+yamlScalar(key),
					"        value: "+yamlScalar(p.ExecEnv[key]),
				)
			}
		}
		if p.ExecInteractiveMode != "" {
			lines = append(lines, "      interactiveMode: "+yamlScalar(p.ExecInteractiveMode))
		}
		return strings.Join(lines, "\n"), nil
	case secret.ClientKey != "" && p.ClientCertificate != "":
		return strings.Join([]string{
			"    client-certificate-data: " + yamlScalar(base64.StdEncoding.EncodeToString([]byte(p.ClientCertificate))),
			"    client-key-data: " + yamlScalar(base64.StdEncoding.EncodeToString([]byte(secret.ClientKey))),
		}, "\n"), nil
	default:
		return "", fmt.Errorf("kube profile %q requires a token or client certificate auth material", p.Name)
	}
}

func kubeExecAPIVersion(p profile.Profile) string {
	if p.ExecAPIVersion != "" {
		return p.ExecAPIVersion
	}
	return "client.authentication.k8s.io/v1beta1"
}

func yamlScalar(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func uniqueKubeName(preferred, fallback string, used map[string]struct{}) string {
	name := strings.TrimSpace(preferred)
	if name == "" {
		name = strings.TrimSpace(fallback)
	}
	if name == "" {
		name = "authrun"
	}
	if _, exists := used[name]; !exists {
		used[name] = struct{}{}
		return name
	}
	base := strings.TrimSpace(fallback)
	if base == "" {
		base = name
	}
	candidate := base
	if _, exists := used[candidate]; !exists {
		used[candidate] = struct{}{}
		return candidate
	}
	for i := 2; ; i++ {
		candidate = fmt.Sprintf("%s-%d", base, i)
		if _, exists := used[candidate]; !exists {
			used[candidate] = struct{}{}
			return candidate
		}
	}
}
