package iam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	iamapi "google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

func TestCustomPermissionSupportUsesDocumentedDefault(t *testing.T) {
	for _, scenario := range []struct {
		name           string
		level          string
		predefinedOnly bool
		apiDisabled    bool
		missing        bool
		allowed        bool
	}{
		{name: "omitted-supported-default", allowed: true},
		{name: "explicit-supported", level: "SUPPORTED", allowed: true},
		{name: "testing", level: "TESTING", allowed: true},
		{name: "not-supported", level: "NOT_SUPPORTED"},
		{name: "unknown-enum", level: "NEW_UNKNOWN_VALUE"},
		{name: "predefined-only", level: "SUPPORTED", predefinedOnly: true},
		{name: "api-disabled", level: "SUPPORTED", apiDisabled: true},
		{name: "missing", missing: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != "POST" || r.URL.Path != "/v1/permissions:queryTestablePermissions" || r.Header.Get("Authorization") != "" {
					t.Error("unexpected RPC or real credential use")
					w.WriteHeader(405)
					return
				}
				var input iamapi.QueryTestablePermissionsRequest
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if input.FullResourceName != "//cloudresourcemanager.googleapis.com/projects/test-project" || input.PageSize != 1000 {
					t.Error("incorrect resource or inefficient page size")
				}
				page := &iamapi.QueryTestablePermissionsResponse{}
				if input.PageToken == "" {
					page.NextPageToken = "second-page"
					page.Permissions = []*iamapi.Permission{{Name: "unrelated.permission.get"}}
				} else {
					if input.PageToken != "second-page" {
						t.Error("pagination token lost")
					}
					if !scenario.missing {
						page.Permissions = []*iamapi.Permission{{Name: "compute.instances.get", CustomRolesSupportLevel: scenario.level, OnlyInPredefinedRoles: scenario.predefinedOnly, ApiDisabled: scenario.apiDisabled}}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()
			c, err := NewClient(t.Context(), "test-project", option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
			if err != nil {
				t.Fatal(err)
			}
			err = c.CheckCustomPermissions(t.Context(), []string{"compute.instances.get"})
			if (err == nil) != scenario.allowed {
				t.Fatalf("allowed=%t error=%v", scenario.allowed, err)
			}
			if requests != 2 {
				t.Fatalf("expected complete two-page read; got %d requests", requests)
			}
		})
	}
}

func TestCustomPermissionLookupHonorsCallerCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("canceled lookup reached HTTP service") }))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c, err := NewClient(t.Context(), "test-project", option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CheckCustomPermissions(ctx, []string{"compute.instances.get"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
