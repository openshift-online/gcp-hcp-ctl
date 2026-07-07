package cluster

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/kubeconfig"
)

func dataPtr(m map[string]any) *map[string]any {
	return &m
}

func TestExtractEndpointFromStatuses(t *testing.T) {
	t.Run("When hc-adapter has apiEndpoint it should return it", func(t *testing.T) {
		wantEndpoint := "https://api.test-cluster.example.com"

		endpoint, err := extractEndpointFromStatuses([]hyperfleet.AdapterStatus{
			{Adapter: "placement-adapter", Data: dataPtr(map[string]any{})},
			{
				Adapter: hcAdapterName,
				Data: dataPtr(map[string]any{
					"hostedCluster": map[string]any{
						"apiEndpoint": wantEndpoint,
					},
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if endpoint != wantEndpoint {
			t.Errorf("got %q, want %q", endpoint, wantEndpoint)
		}
	})

	t.Run("When hc-adapter has no apiEndpoint it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromStatuses([]hyperfleet.AdapterStatus{
			{Adapter: hcAdapterName, Data: dataPtr(map[string]any{})},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("When no hc-adapter exists it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromStatuses([]hyperfleet.AdapterStatus{
			{Adapter: "other-adapter", Data: dataPtr(map[string]any{})},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("When items list is empty it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromStatuses(nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("When apiEndpoint is not HTTPS it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromStatuses([]hyperfleet.AdapterStatus{
			{
				Adapter: hcAdapterName,
				Data: dataPtr(map[string]any{
					"hostedCluster": map[string]any{
						"apiEndpoint": "http://insecure.example.com",
					},
				}),
			},
		})
		if err == nil {
			t.Fatal("expected error for non-HTTPS endpoint, got nil")
		}
	})

	t.Run("When apiEndpoint contains whitespace it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromStatuses([]hyperfleet.AdapterStatus{
			{
				Adapter: hcAdapterName,
				Data: dataPtr(map[string]any{
					"hostedCluster": map[string]any{
						"apiEndpoint": "https:// malicious.com",
					},
				}),
			},
		})
		if err == nil {
			t.Fatal("expected error for URL with whitespace, got nil")
		}
	})

	t.Run("When apiEndpoint has no host it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromStatuses([]hyperfleet.AdapterStatus{
			{
				Adapter: hcAdapterName,
				Data: dataPtr(map[string]any{
					"hostedCluster": map[string]any{
						"apiEndpoint": "https://",
					},
				}),
			},
		})
		if err == nil {
			t.Fatal("expected error for URL without host, got nil")
		}
	})

	t.Run("When hc-adapter data is nil it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromStatuses([]hyperfleet.AdapterStatus{
			{Adapter: hcAdapterName, Data: nil},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

// writeTestKubeconfig creates a kubeconfig at path with the given current-context
// and a matching context entry so RestoreContext can find it.
func writeTestKubeconfig(t *testing.T, path, currentContext string) {
	t.Helper()
	content := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://prev.example.com
  name: %[1]s
contexts:
- context:
    cluster: %[1]s
    user: %[1]s
  name: %[1]s
users:
- name: %[1]s
  user: {}
current-context: %[1]s
`, currentContext)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("creating dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writing kubeconfig: %v", err)
	}
}

// setupLoginWithValidation writes a kubeconfig with a previous context, runs
// kubeconfig.Update to switch to a new context, then calls the shared
// handleValidationFailure helper (the same code path as runLogin).
func setupLoginWithValidation(t *testing.T, previousContext string, validateFn validateAccessFunc) (output string, err error) {
	t.Helper()
	dir := t.TempDir()
	kcPath := filepath.Join(dir, "config")

	if previousContext != "" {
		writeTestKubeconfig(t, kcPath, previousContext)
	}

	contextName, prevCtx, kubeconfigPath, updateErr := kubeconfig.Update(kubeconfig.UpdateOptions{
		ClusterName:    "test-cluster",
		Server:         "https://api.test.example.com",
		KubeconfigPath: kcPath,
	})
	if updateErr != nil {
		t.Fatalf("kubeconfig.Update error: %v", updateErr)
	}

	var buf bytes.Buffer
	_, valErr := validateFn(context.Background(), kubeconfigPath, contextName)
	if valErr != nil {
		err = handleValidationFailure(&buf, valErr, kubeconfigPath, contextName, prevCtx)
		return buf.String(), err
	}
	return buf.String(), nil
}

func TestLoginRollback(t *testing.T) {
	t.Run("When validation fails with previousContext it should restore the previous context", func(t *testing.T) {
		output, err := setupLoginWithValidation(t, "prev-ctx", func(_ context.Context, _, _ string) (string, error) {
			return "", fmt.Errorf("connection refused")
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(output, `Current context restored to "prev-ctx"`) {
			t.Errorf("expected restore message in output, got:\n%s", output)
		}
	})

	t.Run("When validation fails without previousContext it should not attempt restore", func(t *testing.T) {
		output, err := setupLoginWithValidation(t, "", func(_ context.Context, _, _ string) (string, error) {
			return "", fmt.Errorf("connection refused")
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if strings.Contains(output, "restored") {
			t.Errorf("should not attempt restore when no previous context, got:\n%s", output)
		}
		if !strings.Contains(output, "To switch manually") {
			t.Errorf("expected manual switch hint in output, got:\n%s", output)
		}
	})

	t.Run("When RestoreContext itself fails it should report the restore error", func(t *testing.T) {
		dir := t.TempDir()
		kcPath := filepath.Join(dir, "config")

		writeTestKubeconfig(t, kcPath, "original-ctx")

		contextName, prevCtx, kubeconfigPath, err := kubeconfig.Update(kubeconfig.UpdateOptions{
			ClusterName:    "test-cluster",
			Server:         "https://api.test.example.com",
			KubeconfigPath: kcPath,
		})
		if err != nil {
			t.Fatalf("kubeconfig.Update error: %v", err)
		}
		if prevCtx == "" {
			t.Fatal("expected non-empty previous context")
		}

		if removeErr := os.Remove(kubeconfigPath); removeErr != nil {
			t.Fatalf("removing kubeconfig: %v", removeErr)
		}

		var buf bytes.Buffer
		valErr := fmt.Errorf("connection refused")
		_ = handleValidationFailure(&buf, valErr, kubeconfigPath, contextName, prevCtx)

		output := buf.String()
		if !strings.Contains(output, "Could not restore previous context") {
			t.Errorf("expected restore failure message, got:\n%s", output)
		}
		if !strings.Contains(output, prevCtx) {
			t.Errorf("expected previous context name in error, got:\n%s", output)
		}
	})
}
