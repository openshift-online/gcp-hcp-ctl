package kubeconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"k8s.io/client-go/tools/clientcmd"
)

func TestDefaultPath(t *testing.T) {
	t.Run("When KUBECONFIG is set it should return that path", func(t *testing.T) {
		t.Setenv("KUBECONFIG", "/custom/path/config")
		got := DefaultPath()
		if got != "/custom/path/config" {
			t.Errorf("got %q, want /custom/path/config", got)
		}
	})

	t.Run("When KUBECONFIG has multiple entries it should return the first", func(t *testing.T) {
		t.Setenv("KUBECONFIG", "/first/config"+string(os.PathListSeparator)+"/second/config")
		got := DefaultPath()
		if got != "/first/config" {
			t.Errorf("got %q, want /first/config", got)
		}
	})

	t.Run("When KUBECONFIG is empty it should return ~/.kube/config", func(t *testing.T) {
		t.Setenv("KUBECONFIG", "")
		got := DefaultPath()
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("getting user home dir: %v", err)
		}
		want := filepath.Join(home, ".kube", "config")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestUpdate(t *testing.T) {
	t.Run("When creating a new entry it should add cluster, user, and context", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		ctxName, _, resolvedPath, err := Update(UpdateOptions{
			ClusterName:     "my-cluster",
			Server:          "https://api.my-cluster.example.com",
			Namespace:       "default",
			InsecureSkipTLS: true,
			KubeconfigPath:  path,
		})
		if err != nil {
			t.Fatalf("update error: %v", err)
		}
		if ctxName != "my-cluster" {
			t.Errorf("got context name %q, want my-cluster", ctxName)
		}
		if resolvedPath != path {
			t.Errorf("got path %q, want %q", resolvedPath, path)
		}

		cfg, err := clientcmd.LoadFromFile(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}

		cluster, ok := cfg.Clusters["my-cluster"]
		if !ok {
			t.Fatal("cluster 'my-cluster' not found")
		}
		if cluster.Server != "https://api.my-cluster.example.com" {
			t.Errorf("got server %q", cluster.Server)
		}
		if !cluster.InsecureSkipTLSVerify {
			t.Error("expected insecure-skip-tls-verify to be true")
		}

		user, ok := cfg.AuthInfos["my-cluster"]
		if !ok {
			t.Fatal("user 'my-cluster' not found")
		}
		if user.Exec == nil {
			t.Fatal("expected exec config")
		}
		if user.Exec.Command != "bash" {
			t.Errorf("got command %q, want bash", user.Exec.Command)
		}
		if user.Exec.APIVersion != "client.authentication.k8s.io/v1beta1" {
			t.Errorf("got apiVersion %q", user.Exec.APIVersion)
		}

		ctx, ok := cfg.Contexts["my-cluster"]
		if !ok {
			t.Fatal("context 'my-cluster' not found")
		}
		if ctx.Namespace != "default" {
			t.Errorf("got namespace %q", ctx.Namespace)
		}
		if cfg.CurrentContext != "my-cluster" {
			t.Errorf("got current-context %q", cfg.CurrentContext)
		}
	})

	t.Run("When updating an existing entry it should replace it", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:    "my-cluster",
			Server:         "https://old.example.com",
			KubeconfigPath: path,
		}); err != nil {
			t.Fatalf("first update error: %v", err)
		}

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:    "my-cluster",
			Server:         "https://new.example.com",
			Namespace:      "prod",
			KubeconfigPath: path,
		}); err != nil {
			t.Fatalf("second update error: %v", err)
		}

		cfg, err := clientcmd.LoadFromFile(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}

		if len(cfg.Clusters) != 1 {
			t.Errorf("expected 1 cluster after upsert, got %d", len(cfg.Clusters))
		}
		if cfg.Clusters["my-cluster"].Server != "https://new.example.com" {
			t.Errorf("got server %q, want https://new.example.com", cfg.Clusters["my-cluster"].Server)
		}
		if cfg.Contexts["my-cluster"].Namespace != "prod" {
			t.Errorf("got namespace %q, want prod", cfg.Contexts["my-cluster"].Namespace)
		}
	})

	t.Run("When merging with existing entries it should preserve others", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:    "cluster-a",
			Server:         "https://a.example.com",
			KubeconfigPath: path,
		}); err != nil {
			t.Fatalf("first update error: %v", err)
		}

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:    "cluster-b",
			Server:         "https://b.example.com",
			KubeconfigPath: path,
		}); err != nil {
			t.Fatalf("second update error: %v", err)
		}

		cfg, err := clientcmd.LoadFromFile(path)
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

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:    "test",
			Server:         "https://test.example.com",
			KubeconfigPath: path,
		}); err != nil {
			t.Fatalf("update error: %v", err)
		}

		cfg, err := clientcmd.LoadFromFile(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}
		if cfg.Contexts["test"].Namespace != "default" {
			t.Errorf("got namespace %q, want default", cfg.Contexts["test"].Namespace)
		}
	})

	t.Run("When file does not exist it should create it with correct permissions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "subdir", "config")

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:    "test",
			Server:         "https://test.example.com",
			KubeconfigPath: path,
		}); err != nil {
			t.Fatalf("update error: %v", err)
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat error: %v", err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("expected 0600 permissions, got %o", info.Mode().Perm())
		}
	})
}

func TestUpdatePreservesFields(t *testing.T) {
	t.Run("When existing kubeconfig has extra fields it should preserve them after update", func(t *testing.T) {
		content := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://existing.example.com
    certificate-authority-data: dGVzdC1jZXJ0aWZpY2F0ZS1kYXRh
    proxy-url: http://proxy.corp:8080
  name: existing-cluster
users:
- name: existing-user
  user:
    client-certificate-data: dGVzdC1jbGllbnQtY2VydA==
    client-key-data: dGVzdC1jbGllbnQta2V5
- name: oidc-user
  user:
    auth-provider:
      name: oidc
      config:
        idp-issuer-url: https://issuer.example.com
        client-id: my-client
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

		_, _, _, err := Update(UpdateOptions{
			ClusterName:    "new-cluster",
			Server:         "https://new.example.com",
			KubeconfigPath: path,
		})
		if err != nil {
			t.Fatalf("update error: %v", err)
		}

		cfg, err := clientcmd.LoadFromFile(path)
		if err != nil {
			t.Fatalf("reload error: %v", err)
		}

		existing := cfg.Clusters["existing-cluster"]
		if existing == nil {
			t.Fatal("existing-cluster was dropped")
		}
		if existing.ProxyURL != "http://proxy.corp:8080" {
			t.Errorf("proxy-url lost, got %q", existing.ProxyURL)
		}
		if string(existing.CertificateAuthorityData) != "test-certificate-data" {
			t.Errorf("certificate-authority-data lost, got %q", existing.CertificateAuthorityData)
		}

		existingUser := cfg.AuthInfos["existing-user"]
		if existingUser == nil {
			t.Fatal("existing-user was dropped")
		}
		if string(existingUser.ClientCertificateData) != "test-client-cert" {
			t.Error("client-certificate-data was dropped")
		}
		if string(existingUser.ClientKeyData) != "test-client-key" {
			t.Error("client-key-data was dropped")
		}

		oidcUser := cfg.AuthInfos["oidc-user"]
		if oidcUser == nil {
			t.Fatal("oidc-user was dropped")
		}
		if oidcUser.AuthProvider == nil {
			t.Fatal("auth-provider was dropped")
		}
		if oidcUser.AuthProvider.Name != "oidc" {
			t.Errorf("auth-provider name = %q, want oidc", oidcUser.AuthProvider.Name)
		}

		newCluster := cfg.Clusters["new-cluster"]
		if newCluster == nil {
			t.Fatal("new-cluster was not added")
		}
		if newCluster.Server != "https://new.example.com" {
			t.Errorf("new cluster server = %q", newCluster.Server)
		}
	})

	t.Run("When updating an existing cluster entry it should preserve other entries' fields", func(t *testing.T) {
		content := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://managed.example.com
  name: managed-cluster
- cluster:
    server: https://other.example.com
    certificate-authority-data: a2VlcC10aGlzLWNhLWRhdGE=
    tls-server-name: other.internal
  name: other-cluster
users:
- name: managed-cluster
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: gcloud
      args: ["auth", "print-identity-token"]
- name: other-user
  user:
    client-certificate: /path/to/cert.pem
    client-key: /path/to/key.pem
contexts:
- context:
    cluster: managed-cluster
    user: managed-cluster
  name: managed-cluster
- context:
    cluster: other-cluster
    user: other-user
  name: other-context
current-context: other-context
`
		dir := t.TempDir()
		path := filepath.Join(dir, "config")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatalf("writing test kubeconfig: %v", err)
		}

		_, _, _, err := Update(UpdateOptions{
			ClusterName:    "managed-cluster",
			Server:         "https://updated-managed.example.com",
			KubeconfigPath: path,
		})
		if err != nil {
			t.Fatalf("update error: %v", err)
		}

		cfg, err := clientcmd.LoadFromFile(path)
		if err != nil {
			t.Fatalf("reload error: %v", err)
		}

		other := cfg.Clusters["other-cluster"]
		if other == nil {
			t.Fatal("other-cluster was dropped")
		}
		if string(other.CertificateAuthorityData) != "keep-this-ca-data" {
			t.Errorf("ca data lost, got %q", other.CertificateAuthorityData)
		}
		if other.TLSServerName != "other.internal" {
			t.Errorf("tls-server-name lost, got %q", other.TLSServerName)
		}

		otherUser := cfg.AuthInfos["other-user"]
		if otherUser == nil {
			t.Fatal("other-user was dropped")
		}
		if otherUser.ClientCertificate != "/path/to/cert.pem" {
			t.Errorf("client-certificate lost, got %q", otherUser.ClientCertificate)
		}
		if otherUser.ClientKey != "/path/to/key.pem" {
			t.Errorf("client-key lost, got %q", otherUser.ClientKey)
		}

		managed := cfg.Clusters["managed-cluster"]
		if managed == nil {
			t.Fatal("managed-cluster was dropped")
		}
		if managed.Server != "https://updated-managed.example.com" {
			t.Errorf("managed server = %q", managed.Server)
		}
	})
}

func TestUpdateReturnsResolvedPath(t *testing.T) {
	t.Run("When KubeconfigPath is empty it should return the default path", func(t *testing.T) {
		dir := t.TempDir()
		defaultPath := filepath.Join(dir, ".kube", "config")
		t.Setenv("KUBECONFIG", defaultPath)

		_, _, resolvedPath, err := Update(UpdateOptions{
			ClusterName: "test",
			Server:      "https://test.example.com",
		})
		if err != nil {
			t.Fatalf("update error: %v", err)
		}
		if resolvedPath != defaultPath {
			t.Errorf("got path %q, want %q", resolvedPath, defaultPath)
		}
	})
}

func TestUpdateReturnsPreviousContext(t *testing.T) {
	t.Run("When kubeconfig has an existing current-context it should return it", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:    "old-cluster",
			Server:         "https://old.example.com",
			KubeconfigPath: path,
		}); err != nil {
			t.Fatalf("first update error: %v", err)
		}

		_, prevCtx, _, err := Update(UpdateOptions{
			ClusterName:    "new-cluster",
			Server:         "https://new.example.com",
			KubeconfigPath: path,
		})
		if err != nil {
			t.Fatalf("second update error: %v", err)
		}
		if prevCtx != "old-cluster" {
			t.Errorf("got previousContext %q, want old-cluster", prevCtx)
		}
	})

	t.Run("When kubeconfig is new it should return empty previous context", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		_, prevCtx, _, err := Update(UpdateOptions{
			ClusterName:    "first-cluster",
			Server:         "https://first.example.com",
			KubeconfigPath: path,
		})
		if err != nil {
			t.Fatalf("update error: %v", err)
		}
		if prevCtx != "" {
			t.Errorf("got previousContext %q, want empty", prevCtx)
		}
	})
}

func TestUpdateInsecureSkipTLS(t *testing.T) {
	t.Run("When InsecureSkipTLS is true it should clear CertificateAuthorityData", func(t *testing.T) {
		content := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://api.example.com
    certificate-authority-data: dGVzdC1jYS1kYXRh
  name: my-cluster
users:
- name: my-cluster
  user: {}
contexts:
- context:
    cluster: my-cluster
    user: my-cluster
  name: my-cluster
current-context: my-cluster
`
		dir := t.TempDir()
		path := filepath.Join(dir, "config")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatalf("writing test kubeconfig: %v", err)
		}

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:     "my-cluster",
			Server:          "https://api.example.com",
			InsecureSkipTLS: true,
			KubeconfigPath:  path,
		}); err != nil {
			t.Fatalf("update error: %v", err)
		}

		cfg, err := clientcmd.LoadFromFile(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}
		cluster := cfg.Clusters["my-cluster"]
		if !cluster.InsecureSkipTLSVerify {
			t.Error("expected InsecureSkipTLSVerify to be true")
		}
		if len(cluster.CertificateAuthorityData) != 0 {
			t.Errorf("expected CertificateAuthorityData to be cleared, got %q", cluster.CertificateAuthorityData)
		}
		if cluster.CertificateAuthority != "" {
			t.Errorf("expected CertificateAuthority to be cleared, got %q", cluster.CertificateAuthority)
		}
	})

	t.Run("When InsecureSkipTLS is false it should clear a previously set InsecureSkipTLSVerify", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:     "my-cluster",
			Server:          "https://api.example.com",
			InsecureSkipTLS: true,
			KubeconfigPath:  path,
		}); err != nil {
			t.Fatalf("first update error: %v", err)
		}

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:     "my-cluster",
			Server:          "https://api.example.com",
			InsecureSkipTLS: false,
			KubeconfigPath:  path,
		}); err != nil {
			t.Fatalf("second update error: %v", err)
		}

		cfg, err := clientcmd.LoadFromFile(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}
		if cfg.Clusters["my-cluster"].InsecureSkipTLSVerify {
			t.Error("expected InsecureSkipTLSVerify to be false after re-login without the flag")
		}
	})
}

func TestUpdateClearsConflictingAuthFields(t *testing.T) {
	t.Run("When existing user has token credentials it should clear them after setting exec", func(t *testing.T) {
		content := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://api.example.com
  name: my-cluster
users:
- name: my-cluster
  user:
    token: old-bearer-token
    client-certificate-data: dGVzdC1jbGllbnQtY2VydA==
    client-key-data: dGVzdC1jbGllbnQta2V5
contexts:
- context:
    cluster: my-cluster
    user: my-cluster
  name: my-cluster
current-context: my-cluster
`
		dir := t.TempDir()
		path := filepath.Join(dir, "config")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatalf("writing test kubeconfig: %v", err)
		}

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:    "my-cluster",
			Server:         "https://api.example.com",
			KubeconfigPath: path,
		}); err != nil {
			t.Fatalf("update error: %v", err)
		}

		cfg, err := clientcmd.LoadFromFile(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}
		user := cfg.AuthInfos["my-cluster"]
		if user == nil {
			t.Fatal("user 'my-cluster' not found")
		}
		if user.Exec == nil {
			t.Fatal("expected exec config to be set")
		}
		if user.Token != "" {
			t.Errorf("expected token to be cleared, got %q", user.Token)
		}
		if len(user.ClientCertificateData) != 0 {
			t.Error("expected client-certificate-data to be cleared")
		}
		if len(user.ClientKeyData) != 0 {
			t.Error("expected client-key-data to be cleared")
		}
		if user.AuthProvider != nil {
			t.Error("expected auth-provider to be cleared")
		}
	})
}

func TestRestoreContext(t *testing.T) {
	t.Run("When called with a previous context it should restore current-context", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:    "original",
			Server:         "https://original.example.com",
			KubeconfigPath: path,
		}); err != nil {
			t.Fatalf("first update error: %v", err)
		}

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:    "new-context",
			Server:         "https://new.example.com",
			KubeconfigPath: path,
		}); err != nil {
			t.Fatalf("second update error: %v", err)
		}

		if err := RestoreContext(path, "original"); err != nil {
			t.Fatalf("restore context error: %v", err)
		}

		cfg, err := clientcmd.LoadFromFile(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}
		if cfg.CurrentContext != "original" {
			t.Errorf("got current-context %q, want original", cfg.CurrentContext)
		}
	})

	t.Run("When context does not exist it should return an error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:    "only-context",
			Server:         "https://only.example.com",
			KubeconfigPath: path,
		}); err != nil {
			t.Fatalf("update error: %v", err)
		}

		err := RestoreContext(path, "deleted-context")
		if err == nil {
			t.Fatal("expected error for non-existent context, got nil")
		}

		cfg, loadErr := clientcmd.LoadFromFile(path)
		if loadErr != nil {
			t.Fatalf("load error: %v", loadErr)
		}
		if cfg.CurrentContext != "only-context" {
			t.Errorf("current-context should be unchanged, got %q", cfg.CurrentContext)
		}
	})

	t.Run("When called with empty previousContext it should be a no-op", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config")

		if _, _, _, err := Update(UpdateOptions{
			ClusterName:    "test",
			Server:         "https://test.example.com",
			KubeconfigPath: path,
		}); err != nil {
			t.Fatalf("update error: %v", err)
		}

		if err := RestoreContext(path, ""); err != nil {
			t.Fatalf("restore context error: %v", err)
		}

		cfg, err := clientcmd.LoadFromFile(path)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}
		if cfg.CurrentContext != "test" {
			t.Errorf("got current-context %q, want test (unchanged)", cfg.CurrentContext)
		}
	})
}

func TestGcloudExecCredential(t *testing.T) {
	t.Run("When called it should return a valid exec config with gcloud script", func(t *testing.T) {
		exec := gcloudExecCredential()

		if exec.APIVersion != "client.authentication.k8s.io/v1beta1" {
			t.Errorf("got APIVersion %q, want client.authentication.k8s.io/v1beta1", exec.APIVersion)
		}
		if exec.Command != "bash" {
			t.Errorf("got Command %q, want bash", exec.Command)
		}
		if len(exec.Args) != 2 {
			t.Fatalf("got %d args, want 2", len(exec.Args))
		}
		if exec.Args[0] != "-c" {
			t.Errorf("got Args[0] %q, want -c", exec.Args[0])
		}
		if !strings.Contains(exec.Args[1], "gcloud auth print-identity-token") {
			t.Error("script does not contain gcloud auth print-identity-token")
		}
		if !strings.Contains(exec.Args[1], "ExecCredential") {
			t.Error("script does not produce ExecCredential JSON")
		}
		if exec.InteractiveMode != clientcmdapi.NeverExecInteractiveMode {
			t.Errorf("got InteractiveMode %q, want NeverExecInteractiveMode", exec.InteractiveMode)
		}
	})
}
