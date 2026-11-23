package application

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	gcpiam "github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/iam"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/option"
)

func TestRolePermissionUpdateUsesExactMaskAndEtag(t *testing.T) {
	name := "projects/test-project/roles/owned"
	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("test used credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/"+name {
			t.Errorf("unexpected role path: %s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "etag": "etag-before", "description": "owner-proof", "title": "keep-title", "stage": "GA", "includedPermissions": []string{"compute.instances.get"}})
			return
		}
		if r.Method != http.MethodPatch {
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(405)
			return
		}
		patches++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Query().Get("updateMask") != "includedPermissions" || body["etag"] != "etag-before" || len(body) != 2 {
			t.Errorf("role update touched ownership or omitted etag: %s %+v", r.URL.RawQuery, body)
		}
		_, _ = w.Write([]byte(`{"etag":"etag-after"}`))
	}))
	defer server.Close()
	client, err := gcpiam.NewClient(t.Context(), "test-project", option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	cloud := &GCP{Client: client}
	desired := Resource{Kind: "Role", Name: name, UID: "etag-before", Owner: "owner-proof", Stage: "GA", Permissions: []string{"compute.instances.get", "compute.instances.start"}}
	if err := cloud.UpdateRolePermissions(t.Context(), desired); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Resource{
		{Kind: "Role", Name: name, UID: "wrong-etag", Owner: "owner-proof", Stage: "GA", Permissions: slices.Clone(desired.Permissions)},
		{Kind: "Role", Name: name, UID: "etag-before", Owner: "foreign", Stage: "GA", Permissions: slices.Clone(desired.Permissions)},
		{Kind: "Role", Name: name, UID: "etag-before", Owner: "owner-proof", Stage: "GA", Permissions: []string{"compute.instances.start"}},
	} {
		if err := cloud.UpdateRolePermissions(t.Context(), bad); err == nil {
			t.Fatal("unsafe permissions update accepted")
		}
	}
	if patches != 1 {
		t.Fatalf("sent %d patches, expected only reviewed additive update", patches)
	}
}

func TestSDKBackendReadbackTrustAndPolicyCASOverHTTP(t *testing.T) {
	p := testPlan(t)
	j, err := newJournal(p)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Resource{}
	for _, r := range p.Resources {
		r.Owner = j.Owner()
		byName[r.Name] = r
	}
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "" {
			t.Error("test used real credentials")
		}
		name := strings.TrimPrefix(r.URL.Path, "/v1/")
		switch {
		case strings.HasSuffix(name, ":getIamPolicy"):
			_ = json.NewEncoder(w).Encode(map[string]any{"version": 3, "etag": "policy-etag", "bindings": []map[string]any{{"role": "roles/viewer", "members": []string{"user:foreign@example.test"}, "condition": map[string]any{"expression": "true", "title": "preserve"}}}, "auditConfigs": []map[string]any{{"service": "allServices"}}})
			return
		case strings.HasSuffix(name, ":setIamPolicy"):
			mutations++
			var body struct {
				Policy crm.Policy `json:"policy"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Policy.Etag != "policy-etag" || body.Policy.Version != 3 || body.Policy.Bindings[0].Condition == nil || len(body.Policy.AuditConfigs) != 1 {
				t.Error("SDK policy mapping discarded CAS or unrelated fields")
			}
			_ = json.NewEncoder(w).Encode(body.Policy)
			return
		case name == "projects/test-project":
			_ = json.NewEncoder(w).Encode(map[string]any{"projectNumber": "123456789"})
			return
		}
		if resource, ok := byName[name]; ok && r.Method == "GET" {
			switch resource.Kind {
			case "ServiceAccount":
				_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "email": strings.TrimPrefix(name, "projects/test-project/serviceAccounts/"), "projectId": "test-project", "uniqueId": "account-uid", "description": resource.Owner})
			case "Role":
				_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "etag": "role-etag", "description": resource.Owner, "includedPermissions": resource.Permissions, "stage": "GA"})
			case "Pool":
				_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "state": "ACTIVE", "description": resource.Owner})
			case "Provider":
				_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "state": "ACTIVE", "description": resource.Owner, "oidc": map[string]any{"issuerUri": resource.Issuer, "allowedAudiences": resource.Audiences}, "attributeMapping": resource.Mapping})
			}
			return
		}
		t.Errorf("unexpected SDK RPC: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(400)
	}))
	defer server.Close()
	client, err := gcpiam.NewClient(t.Context(), p.Identity.Project, option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	cloud := &GCP{Client: client}
	if number, err := cloud.ProjectNumber(t.Context()); err != nil || number != p.Identity.ProjectNumber {
		t.Fatalf("project readback: %v", err)
	}
	for _, want := range byName {
		got, err := cloud.Get(t.Context(), want)
		if err != nil {
			t.Fatal(err)
		}
		if !sameResource(want, *got) {
			t.Fatalf("SDK changed desired shape: %s", want.Kind)
		}
	}
	if mutations != 0 {
		t.Fatal("resource readback mutated cloud")
	}
	for _, scope := range []string{"project", "projects/test-project/serviceAccounts/fresh-123-nodepool-mgmt@test-project.iam.gserviceaccount.com"} {
		policy, err := cloud.GetPolicy(t.Context(), scope)
		if err != nil {
			t.Fatal(err)
		}
		if err := cloud.SetPolicy(t.Context(), scope, policy); err != nil {
			t.Fatal(err)
		}
	}
	if mutations != 2 {
		t.Fatal("policy mutation count differs")
	}
}
