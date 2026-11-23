package application

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAuthenticatedCatalogPinAndEligibility(t *testing.T) {
	for _, scenario := range []string{"ready", "unverified", "false", "unknown", "stale", "wrong-uid", "wrong-digest", "wrong-channel", "unsupported", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := testTarget(t)
			version := &v1.Version{ObjectMeta: metav1.ObjectMeta{Name: fixture.Version, UID: "version-uid", Generation: 2}, Spec: v1.VersionSpec{WIFConfigTemplateRef: &fixture.Reference, ChannelGroups: []string{"candidate"}}, Status: v1.VersionStatus{Conditions: []metav1.Condition{{Type: "Available", Status: metav1.ConditionTrue, ObservedGeneration: 2}}}}
			w := &v1.WIFConfigTemplate{ObjectMeta: metav1.ObjectMeta{Name: fixture.Reference.Name, UID: "template-uid"}, Spec: fixture.Template}
			switch scenario {
			case "unverified":
				w.Spec.Verified = new(false)
			case "false":
				version.Status.Conditions[0].Status = metav1.ConditionFalse
			case "unknown":
				version.Status.Conditions[0].Status = metav1.ConditionUnknown
			case "stale":
				version.Status.Conditions[0].ObservedGeneration = 1
			case "wrong-uid":
				w.UID = "replacement"
			case "wrong-digest":
				fixture.Reference.Digest = "sha256:bad"
			case "wrong-channel":
				version.Spec.ChannelGroups = []string{"stable"}
			case "unsupported":
				w.Spec.SchemaVersion = 2
			}
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes++
					rw.WriteHeader(405)
					return
				}
				rw.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/apis/gcp.managed.openshift.io/v1/versions/"+fixture.Version {
					_ = json.NewEncoder(rw).Encode(version)
					return
				}
				if scenario == "missing" {
					rw.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(rw).Encode(w)
			}))
			defer server.Close()
			client, err := platformapi.NewClientForTest(server.URL, "test-project")
			if err != nil {
				t.Fatal(err)
			}
			target, err := Resolve(t.Context(), client, fixture.Version, "candidate")
			switch scenario {
			case "ready":
				if err != nil || target.Eligible() != nil {
					t.Fatalf("ready rejected: %v", err)
				}
			case "unverified", "false", "unknown", "stale":
				if err != nil || target.Eligible() == nil {
					t.Fatalf("blocked target was not inspectable/non-executable: %v", err)
				}
			default:
				if err == nil {
					t.Fatal("invalid pin or catalog accepted")
				}
			}
			if writes != 0 {
				t.Fatal("catalog read issued a mutation")
			}
		})
	}
}
