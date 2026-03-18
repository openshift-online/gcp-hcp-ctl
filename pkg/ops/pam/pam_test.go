package pam

import (
	"bytes"
	"context"
	"testing"
	"time"

	pamclient "github.com/ckandag/gcp-hcp-cli/pkg/gcp/pam"
)

func TestNewPamCmd(t *testing.T) {
	cmd := NewPamCmd()

	if cmd.Use != "pam" {
		t.Errorf("Use = %q, want %q", cmd.Use, "pam")
	}

	subcommands := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subcommands[sub.Name()] = true
	}

	expected := []string{"request", "approve", "deny", "revoke", "status", "list"}
	for _, name := range expected {
		if !subcommands[name] {
			t.Errorf("expected subcommand %q not found", name)
		}
	}

	if len(cmd.Commands()) != len(expected) {
		t.Errorf("expected %d subcommands, got %d", len(expected), len(cmd.Commands()))
	}
}

func TestResolveEntitlementName(t *testing.T) {
	tests := []struct {
		project string
		entID   string
		want    string
	}{
		{
			project: "my-proj",
			entID:   "wf-invoker",
			want:    "projects/my-proj/locations/global/entitlements/wf-invoker",
		},
		{
			project: "my-proj",
			entID:   "projects/other/locations/global/entitlements/test",
			want:    "projects/other/locations/global/entitlements/test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.entID, func(t *testing.T) {
			got := resolveEntitlementName(tt.project, tt.entID)
			if got != tt.want {
				t.Errorf("resolveEntitlementName(%q, %q) = %q, want %q",
					tt.project, tt.entID, got, tt.want)
			}
		})
	}
}

func TestResolveGrantName_FullPath(t *testing.T) {
	fullPath := "projects/p/locations/global/entitlements/e/grants/g1"
	got, err := resolveGrantName(context.TODO(), nil, "p", "", fullPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != fullPath {
		t.Errorf("resolveGrantName with full path = %q, want %q", got, fullPath)
	}
}

func TestResolveGrantName_WithEntitlement(t *testing.T) {
	got, err := resolveGrantName(context.TODO(), nil, "my-proj", "wf-invoker", "grant-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "projects/my-proj/locations/global/entitlements/wf-invoker/grants/grant-123"
	if got != want {
		t.Errorf("resolveGrantName = %q, want %q", got, want)
	}
}

func TestPrintGrantResult_Text(t *testing.T) {
	var buf bytes.Buffer
	grant := &pamclient.GrantInfo{
		Name:        "projects/p/locations/global/entitlements/e/grants/abc123",
		State:       "ACTIVE",
		Requester:   "user@example.com",
		Entitlement: "projects/p/locations/global/entitlements/e",
		RequestedDuration: time.Hour,
		CreateTime:  time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC),
	}

	err := printGrantResult(&buf, "text", grant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"GRANT STATUS", "abc123", "ACTIVE", "user@example.com", "1h0m0s", "e"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintGrantResult_JSON(t *testing.T) {
	var buf bytes.Buffer
	grant := &pamclient.GrantInfo{
		Name:  "projects/p/locations/global/entitlements/e/grants/abc123",
		State: "ACTIVE",
	}

	err := printGrantResult(&buf, "json", grant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !bytes.Contains([]byte(out), []byte(`"state": "ACTIVE"`)) {
		t.Errorf("JSON output missing state:\n%s", out)
	}
}

func TestPrintGrantResult_WithActivateTime(t *testing.T) {
	var buf bytes.Buffer
	grant := &pamclient.GrantInfo{
		Name:         "projects/p/locations/global/entitlements/e/grants/abc123",
		State:        "ACTIVE",
		Requester:    "user@example.com",
		Entitlement:  "projects/p/locations/global/entitlements/e",
		RequestedDuration: time.Hour,
		CreateTime:        time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC),
		ActivateTime:      time.Date(2026, 3, 18, 10, 5, 0, 0, time.UTC),
	}

	err := printGrantResult(&buf, "text", grant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !bytes.Contains([]byte(out), []byte("Activated:")) {
		t.Errorf("output missing Activated line:\n%s", out)
	}
}

func TestRequestCmd_Flags(t *testing.T) {
	cmd := newRequestCmd()

	flags := []string{"duration", "reason", "wait", "timeout"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected flag %q not found", name)
		}
	}
}

func TestListCmd_Flags(t *testing.T) {
	cmd := newListCmd()

	flags := []string{"entitlement", "mine", "approvals", "state", "timeout"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected flag %q not found", name)
		}
	}

	// Check default state value
	stateFlag := cmd.Flags().Lookup("state")
	if stateFlag.DefValue != "ACTIVE,APPROVAL_AWAITED" {
		t.Errorf("state default = %q, want %q", stateFlag.DefValue, "ACTIVE,APPROVAL_AWAITED")
	}
}

func TestApproveCmd_Flags(t *testing.T) {
	cmd := newApproveCmd()
	if cmd.Flags().Lookup("reason") == nil {
		t.Error("expected flag 'reason' not found")
	}
	if cmd.Flags().Lookup("entitlement") == nil {
		t.Error("expected flag 'entitlement' not found")
	}
}

func TestDenyCmd_Flags(t *testing.T) {
	cmd := newDenyCmd()
	if cmd.Flags().Lookup("reason") == nil {
		t.Error("expected flag 'reason' not found")
	}
	if cmd.Flags().Lookup("entitlement") == nil {
		t.Error("expected flag 'entitlement' not found")
	}
}

func TestRevokeCmd_Flags(t *testing.T) {
	cmd := newRevokeCmd()
	if cmd.Flags().Lookup("reason") == nil {
		t.Error("expected flag 'reason' not found")
	}
	if cmd.Flags().Lookup("entitlement") == nil {
		t.Error("expected flag 'entitlement' not found")
	}
}

func TestStatusCmd_Flags(t *testing.T) {
	cmd := newStatusCmd()
	if cmd.Flags().Lookup("entitlement") == nil {
		t.Error("expected flag 'entitlement' not found")
	}
}
