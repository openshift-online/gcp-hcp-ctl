package iam

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
)

func TestUpdateCommandFailsClosedBeforeCloudAccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no confirmation", []string{"owned", "--version=5.0.1", "--channel-group=candidate", "--installation-state=journal.json"}, "choose --dry-run or --confirm"},
		{"confirm without fingerprint", []string{"owned", "--confirm", "--version=5.0.1", "--channel-group=candidate", "--installation-state=journal.json"}, "--expect-plan"},
		{"missing target", []string{"owned", "--dry-run", "--channel-group=candidate", "--installation-state=journal.json"}, "--version"},
		{"missing group", []string{"owned", "--dry-run", "--version=5.0.1", "--installation-state=journal.json"}, "--channel-group"},
		{"missing journal", []string{"owned", "--dry-run", "--version=5.0.1", "--channel-group=candidate"}, "--installation-state"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newUpdateCommand(&config.Config{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unsafe update command accepted: %v", err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "not-created.json")
	cmd := newUpdateCommand(&config.Config{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"owned", "--dry-run", "--version=5.0.1", "--channel-group=candidate", "--installation-state=" + path})
	if err := cmd.Execute(); err == nil {
		t.Fatal("missing original protected journal was accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("preview created an operation file: %v", err)
	}
}

func TestUpdateCommandHasNoBroadBypassFlags(t *testing.T) {
	cmd := newUpdateCommand(&config.Config{})
	for _, flag := range []string{"yes", "force", "skip-verify", "update-state"} {
		if cmd.Flags().Lookup(flag) != nil {
			t.Fatalf("unimplemented IAM mutation flag %q is exposed", flag)
		}
	}
}
