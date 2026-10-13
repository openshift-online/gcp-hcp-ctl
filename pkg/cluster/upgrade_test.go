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
)

func TestNewControlPlaneUpgradeRequest(t *testing.T) {
	request := newControlPlaneUpgradeRequest("customer-project", "test-cluster", "4.22.16")
	if request.APIVersion != "gcp.managed.openshift.io/v1" || request.Kind != "ControlPlaneUpgradeRequest" {
		t.Fatalf("unexpected request type: %s %s", request.APIVersion, request.Kind)
	}
	if request.Namespace != "customer-project" || request.GenerateName != "manual-upgrade-" {
		t.Fatalf("unexpected request metadata: %+v", request.ObjectMeta)
	}
	if request.Spec.ClusterID != "test-cluster" || request.Spec.TargetVersion != "4.22.16" {
		t.Fatalf("unexpected request spec: %+v", request.Spec)
	}
}

func TestUpgradeControlPlaneRequiresVersion(t *testing.T) {
	cmd := newUpgradeControlPlaneCmd()
	cmd.SetArgs([]string{"test-cluster"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected required version error, got %v", err)
	}
}

func TestUpgradeControlPlaneCreatesRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/apis/gcp.managed.openshift.io/v1/namespaces/customer-project/clusters/test-cluster/controlplaneupgraderequests" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var request platformapi.ControlPlaneUpgradeRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if request.Spec.ClusterID != "test-cluster" || request.Spec.TargetVersion != "4.22.16" {
			t.Errorf("unexpected request spec: %+v", request.Spec)
		}
		request.Name = "manual-upgrade-abcde"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(request); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	defer server.Close()
	client, err := platformapi.NewClientForTest(server.URL, "customer-project")
	if err != nil {
		t.Fatal(err)
	}
	cmd := newUpgradeControlPlaneCmd()
	cmd.SetContext(context.WithValue(context.Background(), clientKey, client))
	cmd.SetArgs([]string{"test-cluster", "--version", "4.22.16"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "manual-upgrade-abcde") {
		t.Fatalf("expected created request name in output: %s", out.String())
	}
}

func TestPrintControlPlaneUpgradeRequest(t *testing.T) {
	request := newControlPlaneUpgradeRequest("customer-project", "test-cluster", "4.22.16")
	request.Name = "manual-upgrade-abcde"
	for _, tt := range []struct {
		format string
		want   []string
	}{
		{format: "text", want: []string{"manual-upgrade-abcde", "test-cluster", "4.22.16"}},
		{format: "json", want: []string{`"kind": "ControlPlaneUpgradeRequest"`, `"targetVersion": "4.22.16"`}},
		{format: "yaml", want: []string{"kind: ControlPlaneUpgradeRequest", "targetVersion: 4.22.16"}},
	} {
		t.Run(tt.format, func(t *testing.T) {
			var out bytes.Buffer
			if err := printControlPlaneUpgradeRequest(&out, request, tt.format); err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("%s output missing %q: %s", tt.format, want, out.String())
				}
			}
		})
	}
}
