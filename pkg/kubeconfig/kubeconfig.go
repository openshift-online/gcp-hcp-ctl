// Package kubeconfig provides utilities for reading, writing, and updating
// kubeconfig files with exec-based authentication for GCP hosted clusters.
//
// It delegates to k8s.io/client-go/tools/clientcmd which handles the full
// kubeconfig schema, so no fields are ever lost during round-trips.
package kubeconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// DefaultPath returns the default kubeconfig file path, honoring the
// KUBECONFIG environment variable (using the first entry if multiple
// paths are specified).
func DefaultPath() string {
	if p := os.Getenv("KUBECONFIG"); p != "" {
		parts := filepath.SplitList(p)
		return parts[0]
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".kube", "config")
	}
	return filepath.Join(home, ".kube", "config")
}

// UpdateOptions controls how a kubeconfig entry is created or updated.
type UpdateOptions struct {
	ClusterName     string
	Server          string
	Namespace       string
	InsecureSkipTLS bool
	KubeconfigPath  string
}

// Update merges a new cluster/user/context entry into the kubeconfig at the
// given path. The user entry uses a gcloud exec plugin so kubectl
// automatically refreshes the identity token on every invocation.
// Other entries in the kubeconfig are preserved; the target entry's
// auth fields are replaced with exec-based credentials.
//
// Returns the context name, the previous current-context (for rollback),
// and the resolved kubeconfig path.
func Update(opts UpdateOptions) (contextName, previousContext, kubeconfigPath string, err error) {
	path := opts.KubeconfigPath
	if path == "" {
		path = DefaultPath()
	}

	cfg, err := loadOrNew(path)
	if err != nil {
		return "", "", path, err
	}

	prevCtx := cfg.CurrentContext

	cluster := cfg.Clusters[opts.ClusterName]
	if cluster == nil {
		cluster = &clientcmdapi.Cluster{}
		cfg.Clusters[opts.ClusterName] = cluster
	}
	cluster.Server = opts.Server
	if opts.InsecureSkipTLS {
		cluster.InsecureSkipTLSVerify = true
		cluster.CertificateAuthority = ""
		cluster.CertificateAuthorityData = nil
	} else {
		cluster.InsecureSkipTLSVerify = false
	}

	cfg.AuthInfos[opts.ClusterName] = &clientcmdapi.AuthInfo{
		Exec: gcloudExecCredential(),
	}

	ns := opts.Namespace
	if ns == "" {
		ns = "default"
	}
	ctxName := opts.ClusterName
	ctxEntry := cfg.Contexts[ctxName]
	if ctxEntry == nil {
		ctxEntry = &clientcmdapi.Context{}
		cfg.Contexts[ctxName] = ctxEntry
	}
	ctxEntry.Cluster = opts.ClusterName
	ctxEntry.AuthInfo = opts.ClusterName
	ctxEntry.Namespace = ns
	cfg.CurrentContext = ctxName

	if err := save(cfg, path); err != nil {
		return "", "", path, err
	}
	return ctxName, prevCtx, path, nil
}

// RestoreContext sets current-context back to the given value. If
// previousContext is empty the call is a no-op. Returns an error if the
// target context does not exist in the kubeconfig.
func RestoreContext(kubeconfigPath, previousContext string) error {
	if previousContext == "" {
		return nil
	}
	path := kubeconfigPath
	if path == "" {
		path = DefaultPath()
	}
	cfg, err := loadOrNew(path)
	if err != nil {
		return fmt.Errorf("restoring context: %w", err)
	}
	if _, exists := cfg.Contexts[previousContext]; !exists {
		return fmt.Errorf("cannot restore context %q: context no longer exists in kubeconfig", previousContext)
	}
	cfg.CurrentContext = previousContext
	return save(cfg, path)
}

// loadOrNew reads a kubeconfig file, returning a new empty config if the
// file does not exist.
func loadOrNew(path string) (*clientcmdapi.Config, error) {
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return clientcmdapi.NewConfig(), nil
		}
		return nil, fmt.Errorf("loading kubeconfig %s: %w", path, err)
	}
	return cfg, nil
}

// save writes the config to the given path, creating parent directories
// as needed and restricting permissions to owner-only.
func save(cfg *clientcmdapi.Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating kubeconfig directory: %w", err)
	}
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		return fmt.Errorf("writing kubeconfig %s: %w", path, err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("setting kubeconfig permissions: %w", err)
	}
	return nil
}

// gcloudExecCredential returns an exec plugin config that produces a
// valid client.authentication.k8s.io/v1beta1 ExecCredential by wrapping
// the output of "gcloud auth print-identity-token" in the required JSON
// envelope. This ensures kubectl gets a fresh token on every invocation.
func gcloudExecCredential() *clientcmdapi.ExecConfig {
	return &clientcmdapi.ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1beta1",
		Command:    "bash",
		Args: []string{
			"-c",
			`token="$(gcloud auth print-identity-token)" || exit $?; printf '{"apiVersion":"client.authentication.k8s.io/v1beta1","kind":"ExecCredential","status":{"token":"%s"}}' "$token"`,
		},
		InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
	}
}
