package ops

import (
	"testing"
)

func TestNewRolloutRestartCmd(t *testing.T) {
	cmd := newRolloutRestartCmd()

	if cmd.Use != "rollout-restart <resource-type> <name>" {
		t.Errorf("expected Use='rollout-restart <resource-type> <name>', got %q", cmd.Use)
	}

	// Verify required flags exist
	ns := cmd.Flag("namespace")
	if ns == nil {
		t.Fatal("expected --namespace flag")
	}
	if ns.Shorthand != "n" {
		t.Errorf("expected -n shorthand for namespace, got %q", ns.Shorthand)
	}

	timeout := cmd.Flag("timeout")
	if timeout == nil {
		t.Fatal("expected --timeout flag")
	}
	if timeout.DefValue != "2m0s" {
		t.Errorf("expected default timeout 2m0s, got %q", timeout.DefValue)
	}
}

func TestRolloutRestartCmd_RequiresArgs(t *testing.T) {
	cmd := newRolloutRestartCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	// No args should fail
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with no args")
	}
}

func TestRolloutRestartCmd_RequiresExactlyTwoArgs(t *testing.T) {
	cmd := newRolloutRestartCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	// One arg should fail
	cmd.SetArgs([]string{"deployments"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with only one arg")
	}
}

func TestRolloutRestartCmd_ResourceTypeAliases(t *testing.T) {
	// Verify the aliases used in rollout-restart examples are in resourceTypeExpand
	aliases := map[string]string{
		"deploy": "deployments",
		"sts":    "statefulsets",
		"ds":     "daemonsets",
	}
	for alias, want := range aliases {
		got, ok := resourceTypeExpand[alias]
		if !ok {
			t.Errorf("alias %q not found in resourceTypeExpand", alias)
			continue
		}
		if got != want {
			t.Errorf("resourceTypeExpand[%q] = %q, want %q", alias, got, want)
		}
	}
}
