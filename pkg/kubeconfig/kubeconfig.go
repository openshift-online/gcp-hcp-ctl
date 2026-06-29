// Package kubeconfig provides utilities for reading, writing, and updating
// kubeconfig files with exec-based authentication for GCP hosted clusters.
package kubeconfig

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents a minimal kubeconfig structure.
type Config struct {
	APIVersion     string          `yaml:"apiVersion"`
	Kind           string          `yaml:"kind"`
	Clusters       []NamedCluster  `yaml:"clusters"`
	Users          []NamedUser     `yaml:"users"`
	Contexts       []NamedContext  `yaml:"contexts"`
	CurrentContext string          `yaml:"current-context"`
	Preferences    map[string]any  `yaml:"preferences,omitempty"`
}

// NamedCluster associates a name with cluster connection parameters.
type NamedCluster struct {
	Name    string  `yaml:"name"`
	Cluster Cluster `yaml:"cluster"`
}

// Cluster contains the server address and TLS settings.
type Cluster struct {
	Server                string `yaml:"server"`
	InsecureSkipTLSVerify bool   `yaml:"insecure-skip-tls-verify,omitempty"`
	CertificateAuthority  string `yaml:"certificate-authority,omitempty"`
}

// NamedUser associates a name with user credentials.
type NamedUser struct {
	Name string `yaml:"name"`
	User User   `yaml:"user"`
}

// User contains authentication configuration.
type User struct {
	Exec *ExecConfig `yaml:"exec,omitempty"`
}

// ExecConfig specifies an external command to produce credentials.
type ExecConfig struct {
	APIVersion      string   `yaml:"apiVersion"`
	Command         string   `yaml:"command"`
	Args            []string `yaml:"args"`
	InteractiveMode string   `yaml:"interactiveMode,omitempty"`
}

// NamedContext associates a name with context parameters.
type NamedContext struct {
	Name    string      `yaml:"name"`
	Context ContextBody `yaml:"context"`
}

// ContextBody binds a cluster and user with an optional default namespace.
type ContextBody struct {
	Cluster   string `yaml:"cluster"`
	User      string `yaml:"user"`
	Namespace string `yaml:"namespace,omitempty"`
}

// DefaultPath returns the default kubeconfig file path, honoring the
// KUBECONFIG environment variable.
func DefaultPath() string {
	if p := os.Getenv("KUBECONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".kube", "config")
	}
	return filepath.Join(home, ".kube", "config")
}

// Load reads and parses a kubeconfig file. If the file does not exist,
// an empty Config is returned.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{
				APIVersion: "v1",
				Kind:       "Config",
			}, nil
		}
		return nil, fmt.Errorf("reading kubeconfig %s: %w", path, err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing kubeconfig %s: %w", path, err)
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = "v1"
	}
	if cfg.Kind == "" {
		cfg.Kind = "Config"
	}
	return cfg, nil
}

// Save writes the config to the given path, creating parent directories
// as needed and restricting permissions to owner-only.
func Save(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating kubeconfig directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling kubeconfig: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing kubeconfig %s: %w", path, err)
	}
	return nil
}

// UpdateOptions controls how a kubeconfig entry is created or updated.
type UpdateOptions struct {
	ClusterName          string
	Server               string
	Namespace            string
	InsecureSkipTLS      bool
	KubeconfigPath       string
}

// ContextName returns the kubeconfig context name derived from the cluster name.
func (o *UpdateOptions) ContextName() string {
	return o.ClusterName
}

// Update merges a new cluster/user/context entry into the kubeconfig at the
// given path. The user entry uses a gcloud exec plugin so kubectl
// automatically refreshes the identity token on every invocation.
func Update(opts UpdateOptions) (contextName string, err error) {
	path := opts.KubeconfigPath
	if path == "" {
		path = DefaultPath()
	}

	cfg, err := Load(path)
	if err != nil {
		return "", err
	}

	clusterEntry := NamedCluster{
		Name: opts.ClusterName,
		Cluster: Cluster{
			Server:                opts.Server,
			InsecureSkipTLSVerify: opts.InsecureSkipTLS,
		},
	}

	userName := opts.ClusterName
	userEntry := NamedUser{
		Name: userName,
		User: User{
			Exec: gcloudExecCredential(),
		},
	}

	ns := opts.Namespace
	if ns == "" {
		ns = "default"
	}
	ctxName := opts.ContextName()
	ctxEntry := NamedContext{
		Name: ctxName,
		Context: ContextBody{
			Cluster:   opts.ClusterName,
			User:      userName,
			Namespace: ns,
		},
	}

	upsertCluster(cfg, clusterEntry)
	upsertUser(cfg, userEntry)
	upsertContext(cfg, ctxEntry)
	cfg.CurrentContext = ctxName

	if err := Save(cfg, path); err != nil {
		return "", err
	}
	return ctxName, nil
}

// ValidateAccess runs "kubectl auth whoami" against the given kubeconfig
// context and returns the output. The context controls cancellation.
func ValidateAccess(ctx context.Context, kubeconfigPath, contextName string) (string, error) {
	args := []string{"auth", "whoami"}
	if kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}
	if contextName != "" {
		args = append(args, "--context", contextName)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		output := strings.TrimSpace(string(out))
		if output != "" {
			return "", fmt.Errorf("kubectl auth whoami failed: %s", output)
		}
		return "", fmt.Errorf("kubectl auth whoami failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gcloudExecCredential returns an exec plugin config that produces a
// valid client.authentication.k8s.io/v1beta1 ExecCredential by wrapping
// the output of "gcloud auth print-identity-token" in the required JSON
// envelope. This ensures kubectl gets a fresh token on every invocation.
func gcloudExecCredential() *ExecConfig {
	return &ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1beta1",
		Command:    "/bin/bash",
		Args: []string{
			"-c",
			`printf '{"apiVersion":"client.authentication.k8s.io/v1beta1","kind":"ExecCredential","status":{"token":"%s"}}' "$(gcloud auth print-identity-token)"`,
		},
		InteractiveMode: "Never",
	}
}

func upsertCluster(cfg *Config, entry NamedCluster) {
	for i, c := range cfg.Clusters {
		if c.Name == entry.Name {
			cfg.Clusters[i] = entry
			return
		}
	}
	cfg.Clusters = append(cfg.Clusters, entry)
}

func upsertUser(cfg *Config, entry NamedUser) {
	for i, u := range cfg.Users {
		if u.Name == entry.Name {
			cfg.Users[i] = entry
			return
		}
	}
	cfg.Users = append(cfg.Users, entry)
}

func upsertContext(cfg *Config, entry NamedContext) {
	for i, c := range cfg.Contexts {
		if c.Name == entry.Name {
			cfg.Contexts[i] = entry
			return
		}
	}
	cfg.Contexts = append(cfg.Contexts, entry)
}
