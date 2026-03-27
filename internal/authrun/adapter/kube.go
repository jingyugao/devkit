package adapter

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
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
	kubeconfigPath, cleanup, err := writeKubeconfig(p, secret)
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
	userSection, err := kubeUserSection(p, secret)
	if err != nil {
		return "", nil, err
	}

	clusterLines := []string{
		"    server: " + yamlScalar(p.Server),
	}
	if p.InsecureSkipTLSVerify {
		clusterLines = append(clusterLines, "    insecure-skip-tls-verify: true")
	} else if p.CertificateAuthority != "" {
		clusterLines = append(clusterLines, "    certificate-authority-data: "+yamlScalar(base64.StdEncoding.EncodeToString([]byte(p.CertificateAuthority))))
	}

	namespaceLine := ""
	if p.Namespace != "" {
		namespaceLine = "    namespace: " + yamlScalar(p.Namespace) + "\n"
	}

	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: %s
  cluster:
%s
contexts:
- name: %s
  context:
    cluster: %s
    user: %s
%scurrent-context: %s
users:
- name: %s
  user:
%s`,
		yamlScalar(p.Cluster),
		strings.Join(clusterLines, "\n"),
		yamlScalar(p.Context),
		yamlScalar(p.Cluster),
		yamlScalar(p.Name),
		namespaceLine,
		yamlScalar(p.Context),
		yamlScalar(p.Name),
		userSection,
	)

	path, cleanup, err := writeTempFile("authrun-kubeconfig-*.yaml", kubeconfig)
	if err != nil {
		return "", nil, fmt.Errorf("create kubeconfig: %w", err)
	}
	return path, cleanup, nil
}

func kubeUserSection(p profile.Profile, secret store.Secret) (string, error) {
	switch {
	case secret.Token != "" && (secret.ClientKey != "" || p.ClientCertificate != ""):
		return "", fmt.Errorf("kube profile %q cannot mix token auth with client certificate auth", p.Name)
	case secret.Token != "":
		return "    token: " + yamlScalar(secret.Token), nil
	case secret.ClientKey != "" && p.ClientCertificate != "":
		return strings.Join([]string{
			"    client-certificate-data: " + yamlScalar(base64.StdEncoding.EncodeToString([]byte(p.ClientCertificate))),
			"    client-key-data: " + yamlScalar(base64.StdEncoding.EncodeToString([]byte(secret.ClientKey))),
		}, "\n"), nil
	default:
		return "", fmt.Errorf("kube profile %q requires a token or client certificate auth material", p.Name)
	}
}

func yamlScalar(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
