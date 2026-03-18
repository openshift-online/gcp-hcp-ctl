package pam

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestShortEntitlementName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "projects/my-proj/locations/us-central1/entitlements/wf-invoker",
			want:  "wf-invoker",
		},
		{
			input: "projects/my-proj/locations/global/entitlements/wf-invoker",
			want:  "wf-invoker",
		},
		{
			input: "wf-invoker",
			want:  "wf-invoker",
		},
		{
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ShortEntitlementName(tt.input)
			if got != tt.want {
				t.Errorf("ShortEntitlementName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGrantInfoShortName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{
			name: "projects/my-proj/locations/global/entitlements/wf-invoker/grants/abc123",
			want: "abc123",
		},
		{
			name: "abc123",
			want: "abc123",
		},
		{
			name: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &GrantInfo{Name: tt.name}
			if got := g.ShortName(); got != tt.want {
				t.Errorf("ShortName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGrantInfoShortEntitlement(t *testing.T) {
	g := &GrantInfo{
		Entitlement: "projects/my-proj/locations/global/entitlements/wf-invoker",
	}
	if got := g.ShortEntitlement(); got != "wf-invoker" {
		t.Errorf("ShortEntitlement() = %q, want %q", got, "wf-invoker")
	}
}

func TestEntitlementParent(t *testing.T) {
	c := &Client{Project: "my-proj"}
	got := c.entitlementParent()
	want := "projects/my-proj/locations/global"
	if got != want {
		t.Errorf("entitlementParent() = %q, want %q", got, want)
	}
}

func TestWrapAuthError(t *testing.T) {
	tests := []struct {
		name    string
		action  string
		errMsg  string
		wantSub string
	}{
		{
			name:    "no credentials",
			action:  "test action",
			errMsg:  "could not find default credentials",
			wantSub: "no GCP credentials found",
		},
		{
			name:    "token expired",
			action:  "test action",
			errMsg:  "oauth2: token expired",
			wantSub: "credentials have expired",
		},
		{
			name:    "permission denied",
			action:  "test action",
			errMsg:  "PermissionDenied: access denied",
			wantSub: "permission denied",
		},
		{
			name:    "403 error",
			action:  "test action",
			errMsg:  "403 Forbidden",
			wantSub: "permission denied",
		},
		{
			name:    "not found",
			action:  "test action",
			errMsg:  "NotFound: resource",
			wantSub: "resource not found",
		},
		{
			name:    "unauthenticated",
			action:  "test action",
			errMsg:  "Unauthenticated: bad token",
			wantSub: "authentication failed",
		},
		{
			name:    "401 error",
			action:  "test action",
			errMsg:  "401 Unauthorized",
			wantSub: "authentication failed",
		},
		{
			name:    "generic error",
			action:  "test action",
			errMsg:  "some random error",
			wantSub: "test action: some random error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := wrapAuthError(tt.action, fmt.Errorf("%s", tt.errMsg))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			got := err.Error()
			if !strings.Contains(got, tt.wantSub) {
				t.Errorf("wrapAuthError(%q, %q) = %q, want substring %q", tt.action, tt.errMsg, got, tt.wantSub)
			}
		})
	}
}

func TestGrantInfoFields(t *testing.T) {
	now := time.Now()
	g := &GrantInfo{
		Name:         "projects/p/locations/global/entitlements/e/grants/g1",
		State:        "ACTIVE",
		Requester:    "user@example.com",
		Entitlement:  "projects/p/locations/global/entitlements/e",
		RequestedDuration: time.Hour,
		CreateTime:   now,
		ActivateTime: now.Add(time.Minute),
	}

	if g.ShortName() != "g1" {
		t.Errorf("ShortName() = %q, want %q", g.ShortName(), "g1")
	}
	if g.ShortEntitlement() != "e" {
		t.Errorf("ShortEntitlement() = %q, want %q", g.ShortEntitlement(), "e")
	}
	if g.State != "ACTIVE" {
		t.Errorf("State = %q, want %q", g.State, "ACTIVE")
	}
	if g.ActivateTime.IsZero() {
		t.Error("ActivateTime should not be zero")
	}
}
