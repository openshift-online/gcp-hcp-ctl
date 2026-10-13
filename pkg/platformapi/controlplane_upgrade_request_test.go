package platformapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCreateControlPlaneUpgradeRequest(t *testing.T) {
	const path = "/apis/gcp.managed.openshift.io/v1/namespaces/customer-project/clusters/test-cluster/controlplaneupgraderequests"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != path {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var request ControlPlaneUpgradeRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if request.GenerateName != "manual-upgrade-" || request.Spec.ClusterID != "test-cluster" || request.Spec.TargetVersion != "4.22.16" {
			t.Errorf("unexpected request: %+v", request)
		}
		request.Name = "manual-upgrade-abcde"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(request); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	defer server.Close()

	client, err := NewClientForTest(server.URL, "customer-project")
	if err != nil {
		t.Fatal(err)
	}
	request := &ControlPlaneUpgradeRequest{
		TypeMeta: metav1.TypeMeta{APIVersion: gcpv1.GroupVersion.String(), Kind: "ControlPlaneUpgradeRequest"},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "manual-upgrade-",
			Namespace:    "customer-project",
		},
		Spec: ControlPlaneUpgradeRequestSpec{ClusterID: "test-cluster", TargetVersion: "4.22.16"},
	}
	created, err := client.ControlPlaneUpgradeRequests().Create(context.Background(), "customer-project", "test-cluster", request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "manual-upgrade-abcde" {
		t.Fatalf("created request name = %q", created.Name)
	}
}

func TestCreateControlPlaneUpgradeRequestReturnsValidationError(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Invalid","code":422}`))
	}))
	defer server.Close()
	client, err := NewClientForTest(server.URL, "customer-project")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ControlPlaneUpgradeRequests().Create(context.Background(), "customer-project", "test-cluster", &ControlPlaneUpgradeRequest{
		TypeMeta: metav1.TypeMeta{APIVersion: gcpv1.GroupVersion.String(), Kind: "ControlPlaneUpgradeRequest"},
		Spec:     ControlPlaneUpgradeRequestSpec{ClusterID: "test-cluster", TargetVersion: "4.22.16"},
	})
	if err == nil {
		t.Fatal("expected API validation failure")
	}
	if requests != 1 {
		t.Fatalf("create was attempted %d times, want one", requests)
	}
}
