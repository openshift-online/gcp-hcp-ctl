package iam

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

func TestTypedCloudErrorsPreserveStatusWithoutProviderProse(t *testing.T) {
	for _, code := range []int{403, 404, 409} {
		cause := &googleapi.Error{Code: code, Message: "private-provider-body-403-sensitive"}
		err := wrapAuthError("reading policy", cause)
		var got *googleapi.Error
		if !errors.As(err, &got) || got.Code != code {
			t.Fatal("HTTP status lost or inferred from provider text")
		}
		if strings.Contains(err.Error(), "private-provider") || strings.Contains(err.Error(), "IAM Admin") {
			t.Fatal("provider prose or broad-role advice leaked")
		}
	}
}

func TestPolicyReadsRequestV3WithoutMutations(t *testing.T) {
	reads := 0
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("test unexpectedly acquired real credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, ":getIamPolicy") && strings.Contains(r.URL.Path, "serviceAccounts"):
			reads++
			if r.URL.Query().Get("options.requestedPolicyVersion") != "3" {
				t.Error("SA policy read did not request version 3")
			}
		case strings.HasSuffix(r.URL.Path, ":getIamPolicy"):
			reads++
			var body crm.GetIamPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Options == nil || body.Options.RequestedPolicyVersion != 3 {
				t.Error("project policy read did not request version 3")
			}
		case strings.HasSuffix(r.URL.Path, ":queryTestablePermissions"):
			reads++
			_ = json.NewEncoder(w).Encode(map[string]any{"permissions": []map[string]any{{"name": "compute.instances.get", "customRolesSupportLevel": "SUPPORTED"}}})
			return
		default:
			writes++
			http.Error(w, "unexpected mutator", 405)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": 3, "etag": "policy-etag", "bindings": []map[string]any{{"role": "roles/viewer", "members": []string{"user:unrelated@example.test"}, "condition": map[string]any{"expression": "true", "title": "preserved"}}}})
	}))
	defer server.Close()
	c, err := NewClient(t.Context(), "test-project", option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.GetProjectIAMPolicy(t.Context())
	if err != nil || p.Etag != "policy-etag" || p.Bindings[0].Condition == nil {
		t.Fatalf("project policy lost condition: %v", err)
	}
	s, err := c.GetServiceAccountIAMPolicy(t.Context(), "projects/test-project/serviceAccounts/test@test-project.iam.gserviceaccount.com")
	if err != nil || s.Bindings[0].Condition == nil {
		t.Fatalf("SA policy lost condition: %v", err)
	}
	if err := c.CheckCustomPermissions(t.Context(), []string{"compute.instances.get"}); err != nil {
		t.Fatal(err)
	}
	if reads != 3 || writes != 0 {
		t.Fatalf("read=%d writes=%d", reads, writes)
	}
}
