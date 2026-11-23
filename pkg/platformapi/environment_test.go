package platformapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestResourceAbsenceRequiresExplicitAPIStatus(t *testing.T) {
	for _, scenario := range []string{"api", "proxy", "synthetic", "wrong-reason"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(404)
				if scenario == "proxy" {
					return
				}
				status := metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: metav1.StatusFailure, Reason: metav1.StatusReasonNotFound, Code: 404}
				if scenario == "synthetic" {
					status.Kind = ""
				}
				if scenario == "wrong-reason" {
					status.Reason = metav1.StatusReasonForbidden
				}
				_ = json.NewEncoder(w).Encode(status)
			}))
			defer server.Close()
			client, err := NewClientForTest(server.URL, "test-project")
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Clusters().Get(t.Context(), client.Namespace(), "absent")
			if IsResourceNotFound(err) != (scenario == "api") {
				t.Fatalf("absence classification wrong for %s: %v", scenario, err)
			}
		})
	}
}
func TestAPIRealmNeverPersistsCredentialsOrNonTLSRemote(t *testing.T) {
	for _, value := range []string{"http://remote.example", "https://user:secret@api.example", "https://api.example?token=secret", "https://api.example#secret"} {
		if _, err := CanonicalEndpoint(value); err == nil {
			t.Fatal("unsafe API identity accepted")
		}
	}
	if got, err := CanonicalEndpoint("https://API.example/"); err != nil || got != "https://api.example" {
		t.Fatal("endpoint identity was not canonical")
	}
}
