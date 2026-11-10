package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUpgradeControlPlaneRequiresVersion(t *testing.T) {
	cmd := newUpgradeControlPlaneCmd()
	cmd.SetArgs([]string{"test-cluster"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected required version error, got %v", err)
	}
}

func TestUpgradeControlPlanePatchesCluster(t *testing.T) {
	for _, version := range []string{"4.23", "4.23.9", "5.0.0-ec.6"} {
		for _, format := range []string{"text", "json", "yaml"} {
			t.Run(version+"/"+format, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPatch || r.URL.Path != "/apis/gcp.managed.openshift.io/v1/namespaces/customer-project/clusters/test-cluster" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
						return
					}
					if r.Header.Get("Content-Type") != "application/merge-patch+json" {
						t.Errorf("unexpected patch type: %s", r.Header.Get("Content-Type"))
					}
					var patch map[string]map[string]map[string]string
					if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
						t.Error(err)
						return
					}
					if patch["spec"]["release"]["version"] != version || len(patch["spec"]["release"]) != 1 {
						t.Errorf("unexpected release patch: %+v", patch)
					}
					cluster := &gcpv1.Cluster{TypeMeta: metav1.TypeMeta{APIVersion: gcpv1.GroupVersion.String(), Kind: "Cluster"}, ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"}}
					cluster.Spec.Release.Version = version
					cluster.Status.HostedClusterResult = &gcpv1.HostedClusterResult{Version: "4.22.19"}
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(cluster); err != nil {
						t.Error(err)
					}
				}))
				defer server.Close()
				client, err := platformapi.NewClientForTest(server.URL, "customer-project")
				if err != nil {
					t.Fatal(err)
				}
				cmd := newUpgradeControlPlaneCmd()
				cmd.SetContext(context.WithValue(t.Context(), clientKey, client))
				cmd.SetArgs([]string{"test-cluster", "--version", version, "--output", format})
				var out bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&bytes.Buffer{})
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out.String(), version) || !strings.Contains(out.String(), "test-cluster") {
					t.Fatalf("missing upgrade target: %s", out.String())
				}
				if format == "text" && strings.Contains(out.String(), "started") {
					t.Fatal("saving desired configuration must not claim rollout started")
				}
			})
		}
	}
}

func TestUpgradeControlPlaneRejectsInvalidVersion(t *testing.T) {
	for _, version := range []string{"4", "v4.23.9", "04.23", "4.23oops", "4.23-rc.1", "4.23.9+build"} {
		t.Run(version, func(t *testing.T) {
			cmd := newUpgradeControlPlaneCmd()
			cmd.SetArgs([]string{"test-cluster", "--version", version})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			if err := cmd.Execute(); err == nil {
				t.Fatal("expected invalid version error")
			}
		})
	}
}

func TestUpgradeControlPlaneReturnsAPIError(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		want   string
	}{
		{name: "validation rejected", status: http.StatusBadRequest, want: "invalid request"},
		{name: "release locked", status: http.StatusConflict, want: "conflict"},
		{name: "not authorized", status: http.StatusForbidden, want: "permission denied"},
		{name: "rate limited", status: http.StatusTooManyRequests, want: "too many requests"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			const serverDetail = "private server detail"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodPatch || r.URL.Path != "/apis/gcp.managed.openshift.io/v1/namespaces/customer-project/clusters/test-cluster" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				if err := json.NewEncoder(w).Encode(map[string]string{"message": serverDetail}); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			client, err := platformapi.NewClientForTest(server.URL, "customer-project")
			if err != nil {
				t.Fatal(err)
			}
			cmd := newUpgradeControlPlaneCmd()
			cmd.SetContext(context.WithValue(t.Context(), clientKey, client))
			cmd.SetArgs([]string{"test-cluster", "--version", "4.23"})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			err = cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), serverDetail) || strings.Contains(out.String(), "upgrade requested") {
				t.Fatal("failed request leaked server details or reported success")
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
		})
	}
}
