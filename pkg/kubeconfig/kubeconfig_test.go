package kubeconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	t.Run("When KUBECONFIG is set it should return that path", func(t *testing.T) {
		t.Setenv("KUBECONFIG", "/custom/path/config")
		got := DefaultPath()
		if got != "/custom/path/config" {
			t.Errorf("got %q, want /custom/path/config", got)
		}
	})

	t.Run("When KUBECONFIG is empty it should return ~/.kube/config", func(t *testing.T) {
		t.Setenv("KUBECONFIG", "")
		got := DefaultPath()
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, ".kube", "config")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestLoadNonExistent(t *testing.T) {
	t.Run("When the file does not exist it should return an empty config", func(t *testing.T) {
		cfg, err := Load("/tmp/does-not-exist-kubeconfig-test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.APIVersion != "v1" {
			t.Errorf("got apiVersion %q, want v1", cfg.APIVersion)
		}
		if cfg.Kind != "Config" {
			t.Errorf("got kind %q, want Config", cfg.Kind)
		}
		if len(cfg.Clusters) != 0 {
			t.Errorf("expected no clusters, got %d", len(cfg.Clusters))
		}
	})
}

func TestLoadExisting(t *testing.T) {
	t.Run("When the file exists it should parse it correctly", func(t *testing.T) {
		content := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://existing.example.com
  name: existing-cluster
users:
- name: existing-user
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: gcloud
      args: ["auth", "print-identity-token"]
contexts:
- context:
    cluster: existing-cluster
    user: existing-user
    namespace: kube-system
  name: existing-context
current-context: existing-context
`
		dir := t.TempDir()
		path := filepath.Join(dir, "config")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatalf("writing test kubeconfig: %v", err)
		}

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cfg.Clusters) != 1 {
			t.Fatalf("expected 1 cluster, got %d", len(cfg.Clusters))
		}
		if cfg.Clusters[0].Cluster.Server != "https://existing.example.com" {
			t.Errorf("got server %q", cfg.Clusters[0].Cluster.Server)
		}
		if cfg.CurrentContext != "existing-context" {
			t.Errorf("got current-context %q", cfg.CurrentContext)
		}
	})
}

func TestSaveAndLoad(t *testing.T) {
	t.Run("When saving a config it should round-trip correctly", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "subdir", "config")

		cfg := &Config{
			APIVersion: "v1",
			Kind:       "Config",
			Clusters: []NamedCluster{
				{Name: "test", Cluster: Cluster{Server: "https://test.example.com"}},
			},
		}

		if err := Save(cfg, path); err != nil {
			t.Fatalf("save error: %v", err)
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat error: %v", err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("expected 0600 permissions, got %o", info.Mode().Perm())
		}

		loaded, err := Load(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}
		if len(loaded.Clusters) != 1 || loaded.Clusters[0].Cluster.Server != "https://test.example.com" {
			t.Errorf("round-trip failed: %+v", loaded)
		}
	})
}

func TestUpdate(t *testing.T) {
	t.Run("When creating a new entry it should add cluster, user, and context", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		ctxName, err := Update(UpdateOptions{
			ClusterName:    "my-cluster",
			Server:         "https://api.my-cluster.example.com",
			Namespace:      "default",
			InsecureSkipTLS: true,
			KubeconfigPath: path,
		})
		if err != nil {
			t.Fatalf("update error: %v", err)
		}
		if ctxName != "my-cluster" {
			t.Errorf("got context name %q, want my-cluster", ctxName)
		}

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}

		if len(cfg.Clusters) != 1 {
			t.Fatalf("expected 1 cluster, got %d", len(cfg.Clusters))
		}
		if cfg.Clusters[0].Cluster.Server != "https://api.my-cluster.example.com" {
			t.Errorf("got server %q", cfg.Clusters[0].Cluster.Server)
		}
		if !cfg.Clusters[0].Cluster.InsecureSkipTLSVerify {
			t.Error("expected insecure-skip-tls-verify to be true")
		}

		if len(cfg.Users) != 1 {
			t.Fatalf("expected 1 user, got %d", len(cfg.Users))
		}
		if cfg.Users[0].User.Exec == nil {
			t.Fatal("expected exec config")
		}
		if cfg.Users[0].User.Exec.Command != "/bin/bash" {
			t.Errorf("got command %q, want /bin/bash", cfg.Users[0].User.Exec.Command)
		}
		if cfg.Users[0].User.Exec.APIVersion != "client.authentication.k8s.io/v1beta1" {
			t.Errorf("got apiVersion %q", cfg.Users[0].User.Exec.APIVersion)
		}

		if len(cfg.Contexts) != 1 {
			t.Fatalf("expected 1 context, got %d", len(cfg.Contexts))
		}
		if cfg.Contexts[0].Context.Namespace != "default" {
			t.Errorf("got namespace %q", cfg.Contexts[0].Context.Namespace)
		}
		if cfg.CurrentContext != "my-cluster" {
			t.Errorf("got current-context %q", cfg.CurrentContext)
		}
	})

	t.Run("When updating an existing entry it should replace it", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		_, err := Update(UpdateOptions{
			ClusterName:    "my-cluster",
			Server:         "https://old.example.com",
			KubeconfigPath: path,
		})
		if err != nil {
			t.Fatalf("first update error: %v", err)
		}

		_, err = Update(UpdateOptions{
			ClusterName:    "my-cluster",
			Server:         "https://new.example.com",
			Namespace:      "prod",
			KubeconfigPath: path,
		})
		if err != nil {
			t.Fatalf("second update error: %v", err)
		}

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}

		if len(cfg.Clusters) != 1 {
			t.Errorf("expected 1 cluster after upsert, got %d", len(cfg.Clusters))
		}
		if cfg.Clusters[0].Cluster.Server != "https://new.example.com" {
			t.Errorf("got server %q, want https://new.example.com", cfg.Clusters[0].Cluster.Server)
		}
		if cfg.Contexts[0].Context.Namespace != "prod" {
			t.Errorf("got namespace %q, want prod", cfg.Contexts[0].Context.Namespace)
		}
	})

	t.Run("When merging with existing entries it should preserve others", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		_, err := Update(UpdateOptions{
			ClusterName:    "cluster-a",
			Server:         "https://a.example.com",
			KubeconfigPath: path,
		})
		if err != nil {
			t.Fatalf("first update error: %v", err)
		}

		_, err = Update(UpdateOptions{
			ClusterName:    "cluster-b",
			Server:         "https://b.example.com",
			KubeconfigPath: path,
		})
		if err != nil {
			t.Fatalf("second update error: %v", err)
		}

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}
		if len(cfg.Clusters) != 2 {
			t.Errorf("expected 2 clusters, got %d", len(cfg.Clusters))
		}
		if cfg.CurrentContext != "cluster-b" {
			t.Errorf("got current-context %q, want cluster-b", cfg.CurrentContext)
		}
	})

	t.Run("When namespace is empty it should default to 'default'", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		_, err := Update(UpdateOptions{
			ClusterName:    "test",
			Server:         "https://test.example.com",
			KubeconfigPath: path,
		})
		if err != nil {
			t.Fatalf("update error: %v", err)
		}

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}
		if cfg.Contexts[0].Context.Namespace != "default" {
			t.Errorf("got namespace %q, want default", cfg.Contexts[0].Context.Namespace)
		}
	})
}

func TestContextName(t *testing.T) {
	t.Run("When cluster name is set it should use it as context name", func(t *testing.T) {
		opts := UpdateOptions{ClusterName: "my-cluster"}
		if got := opts.ContextName(); got != "my-cluster" {
			t.Errorf("got %q, want my-cluster", got)
		}
	})
}
