package platformapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
)

func TestIAMApplicationPatchHasUIDRevisionTestsAndNoOtherFields(t *testing.T) {
	for _, code := range []int{200, 409, 412} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPatch || r.URL.Path != "/apis/gcp.managed.openshift.io/v1/namespaces/test-project/clusters/owned" || r.Header.Get("Content-Type") != "application/json-patch+json" {
					t.Errorf("unexpected request %s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
				}
				var ops []map[string]any
				if err := json.NewDecoder(r.Body).Decode(&ops); err != nil {
					t.Error(err)
				}
				if len(ops) != 3 || ops[0]["op"] != "test" || ops[0]["path"] != "/metadata/uid" || ops[0]["value"] != "cluster-uid" || ops[1]["op"] != "test" || ops[1]["path"] != "/metadata/resourceVersion" || ops[1]["value"] != "revision" || ops[2]["op"] != "replace" || ops[2]["path"] != "/spec/platform/gcp/iam/templateApplications" {
					t.Errorf("unbounded or unguarded assertion patch: %+v", ops)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				if code == 200 {
					_, _ = w.Write([]byte(`{"apiVersion":"gcp.managed.openshift.io/v1","kind":"Cluster","metadata":{"name":"owned","uid":"cluster-uid"}}`))
				} else {
					_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","reason":"Conflict","status":"Failure"}`))
				}
			}))
			defer server.Close()
			client, err := NewClientForTest(server.URL, "test-project")
			if err != nil {
				t.Fatal(err)
			}
			apps := []v1.WIFConfigTemplateReference{{Name: "5.0.0", UID: "template-uid", Digest: "sha256:test"}}
			_, err = client.PatchClusterIAMApplications(t.Context(), "test-project", "owned", "cluster-uid", "revision", apps)
			if code == 200 && err != nil {
				t.Fatal(err)
			}
			if code != 200 {
				var cause *HTTPError
				if !errors.As(err, &cause) || cause.StatusCode() != code {
					t.Fatalf("concurrency rejection lost: %v", err)
				}
			}
			if calls != 1 {
				t.Fatalf("assertion patch automatically retried %d times", calls)
			}
			if _, err := client.PatchClusterIAMApplications(t.Context(), "test-project", "bad/name", "cluster-uid", "revision", apps); err == nil || calls != 1 {
				t.Fatalf("unsafe resource name was sent: %v", err)
			}
		})
	}
}
