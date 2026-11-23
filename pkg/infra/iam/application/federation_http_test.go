package application

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gcpiam "github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/iam"
	"google.golang.org/api/option"
)

func TestFederationCreateUsesResourceParentNotCollection(t *testing.T) {
	for _, kind := range []string{"Pool", "Provider"} {
		t.Run(kind, func(t *testing.T) {
			p := testPlan(t)
			var resource Resource
			for _, r := range p.Resources {
				if r.Kind == kind {
					resource = r
				}
			}
			resource.Owner = "owned-operation-marker"
			collection := "/workloadIdentityPools/"
			parameter := "workloadIdentityPoolId"
			if kind == "Provider" {
				collection = "/providers/"
				parameter = "workloadIdentityPoolProviderId"
			}
			parent, id, ok := strings.Cut(resource.Name, collection)
			if !ok {
				t.Fatal("invalid test resource")
			}
			wantPath := "/v1/" + parent + strings.TrimSuffix(collection, "/")
			operation := resource.Name + "/operations/owned-create"
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != wantPath || r.URL.Query().Get(parameter) != id || r.Header.Get("Authorization") != "" {
					t.Errorf("unexpected SDK request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["description"] != resource.Owner || body["displayName"] != id {
					t.Error("ownership marker or identifier changed")
				}
				if kind == "Provider" {
					oidc, ok := body["oidc"].(map[string]any)
					if !ok || oidc["issuerUri"] != resource.Issuer {
						t.Error("provider issuer changed")
					}
					audiences, ok := oidc["allowedAudiences"].([]any)
					if !ok || len(audiences) != len(resource.Audiences) {
						t.Error("provider audience set changed")
					} else {
						for n, audience := range resource.Audiences {
							if audiences[n] != audience {
								t.Error("provider audience changed")
							}
						}
					}
					mapping, ok := body["attributeMapping"].(map[string]any)
					if !ok || mapping["google.subject"] != "assertion.sub" {
						t.Error("provider trust mapping changed")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"name": operation})
			}))
			defer server.Close()
			c, err := gcpiam.NewClient(t.Context(), p.Identity.Project, option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
			if err != nil {
				t.Fatal(err)
			}
			got, err := (&GCP{Client: c}).StartCreate(t.Context(), resource)
			if err != nil || got != operation || calls != 1 {
				t.Fatalf("operation identity not preserved: %q %v calls=%d", got, err, calls)
			}
		})
	}
}

func TestFederationCreateRejectsMalformedNamesBeforeRPC(t *testing.T) {
	for _, r := range []Resource{{Kind: "Pool", Name: "missing-parent"}, {Kind: "Provider", Name: "missing-parent"}, {Kind: "Pool", Name: "projects/123/locations/global/workloadIdentityPools/a/extra"}, {Kind: "Provider", Name: "projects/123/locations/global/workloadIdentityPools/a/providers/"}, {Kind: "Unknown", Name: "invalid"}} {
		if _, err := (&GCP{}).StartCreate(t.Context(), r); err == nil {
			t.Fatal("malformed federation name accepted")
		}
	}
}
