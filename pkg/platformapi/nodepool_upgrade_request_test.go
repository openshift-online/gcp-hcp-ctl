package platformapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const upgradeRequestPath = "/apis/gcp.managed.openshift.io/v1/namespaces/test-project/clusters/test-cluster/nodepoolupgraderequests"

func TestNodePoolUpgradeRequestCreate(t *testing.T) {
	client := testRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != upgradeRequestPath {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["kind"] != "NodePoolUpgradeRequest" || body["apiVersion"] != "gcp.managed.openshift.io/v1" {
			t.Errorf("type metadata = %#v", body)
		}
		if _, found := body["status"]; found {
			t.Error("create body must not include status")
		}
		metadata := body["metadata"].(map[string]any)
		if metadata["generateName"] != "nodepool-upgrade-" || metadata["namespace"] != "test-project" {
			t.Errorf("metadata = %#v", metadata)
		}
		spec := body["spec"].(map[string]any)
		if spec["clusterID"] != "test-cluster" || spec["nodePoolID"] != "workers" || spec["targetVersion"] != "4.22.17" {
			t.Errorf("spec = %#v", spec)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"apiVersion":"gcp.managed.openshift.io/v1","kind":"NodePoolUpgradeRequest","metadata":{"name":"nodepool-upgrade-abcde","namespace":"test-project"},"spec":{"clusterID":"test-cluster","nodePoolID":"workers","targetVersion":"4.22.17"}}`))
	})
	request := &NodePoolUpgradeRequest{
		TypeMeta:   metav1.TypeMeta{APIVersion: "gcp.managed.openshift.io/v1", Kind: "NodePoolUpgradeRequest"},
		ObjectMeta: metav1.ObjectMeta{GenerateName: "nodepool-upgrade-", Namespace: "test-project"},
		Spec:       NodePoolUpgradeRequestSpec{ClusterID: "test-cluster", NodePoolID: "workers", TargetVersion: "4.22.17"},
	}
	created, err := client.NodePoolUpgradeRequests().Create(context.Background(), "test-project", "test-cluster", request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "nodepool-upgrade-abcde" {
		t.Errorf("name = %q", created.Name)
	}
}

func TestNodePoolUpgradeRequestGet(t *testing.T) {
	client := testRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != upgradeRequestPath+"/nodepool-upgrade-abcde" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"gcp.managed.openshift.io/v1","kind":"NodePoolUpgradeRequest","metadata":{"name":"nodepool-upgrade-abcde"},"spec":{"clusterID":"test-cluster","nodePoolID":"workers","targetVersion":"4.22.17"},"status":{"conditions":[{"type":"Completed","status":"True","reason":"UpgradeCompleted","message":"NodePool reached the requested version","lastTransitionTime":"2026-10-20T20:00:00Z"}]}}`))
	})
	request, err := client.NodePoolUpgradeRequests().Get(context.Background(), "test-project", "test-cluster", "nodepool-upgrade-abcde")
	if err != nil {
		t.Fatal(err)
	}
	if request.Status == nil || len(request.Status.Conditions) != 1 || request.Status.Conditions[0].Type != "Completed" {
		t.Errorf("status = %#v", request.Status)
	}
}

func TestNodePoolUpgradeRequestErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		code      int
		body      string
		want      string
		uncertain bool
	}{
		{"forbidden", http.StatusForbidden, `{"message":"private detail"}`, "permission denied", false},
		{"server error", http.StatusServiceUnavailable, `{"message":"private detail"}`, "service unavailable; check the resource before retrying", true},
		{"invalid success response", http.StatusCreated, `{"kind":"Other","metadata":{"name":"wrong"}}`, "server reported success, but its response was invalid; check the resource before retrying", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testRESTClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := client.NodePoolUpgradeRequests().Create(context.Background(), "test-project", "test-cluster", &NodePoolUpgradeRequest{})
			if err == nil || err.Error() != tc.want || IsUncertainOutcome(err) != tc.uncertain {
				t.Errorf("error = %v (uncertain=%v), want %q (uncertain=%v)", err, IsUncertainOutcome(err), tc.want, tc.uncertain)
			}
			if strings.Contains(err.Error(), "private detail") {
				t.Error("server prose leaked")
			}
		})
	}
}
