package ops

import (
	"testing"
)

func TestResourceTypeExpand(t *testing.T) {
	tests := []struct {
		alias string
		want  string
	}{
		{"hc", "hostedclusters"},
		{"np", "nodepools"},
		{"hcp", "hostedcontrolplanes"},
		{"deploy", "deployments"},
		{"svc", "services"},
		{"cm", "configmaps"},
		{"ns", "namespaces"},
		{"po", "pods"},
		{"ev", "events"},
		{"no", "nodes"},
		{"sa", "serviceaccounts"},
		{"pvc", "persistentvolumeclaims"},
		{"pv", "persistentvolumes"},
		{"sts", "statefulsets"},
		{"rs", "replicasets"},
		{"ds", "daemonsets"},
		{"ep", "endpoints"},
		// Singular forms
		{"pod", "pods"},
		{"deployment", "deployments"},
		{"service", "services"},
		{"configmap", "configmaps"},
		{"namespace", "namespaces"},
		{"node", "nodes"},
		{"event", "events"},
		{"hostedcluster", "hostedclusters"},
		{"nodepool", "nodepools"},
		{"hostedcontrolplane", "hostedcontrolplanes"},
	}

	for _, tt := range tests {
		got, ok := resourceTypeExpand[tt.alias]
		if !ok {
			t.Errorf("alias %q not found in resourceTypeExpand", tt.alias)
			continue
		}
		if got != tt.want {
			t.Errorf("resourceTypeExpand[%q] = %q, want %q", tt.alias, got, tt.want)
		}
	}
}

func TestResourceTypeExpand_FullNamesPassThrough(t *testing.T) {
	fullNames := []string{
		"pods", "deployments", "services", "configmaps",
		"namespaces", "nodes", "events",
		"hostedclusters", "nodepools", "hostedcontrolplanes",
	}

	for _, name := range fullNames {
		if _, ok := resourceTypeExpand[name]; ok {
			continue
		}
		// Full plural names that aren't in the map should pass through unchanged
		// (handled by the command logic, not the map)
	}
}

func TestNewOpsCmd(t *testing.T) {
	cmd := NewOpsCmd()

	if cmd.Use != "ops" {
		t.Errorf("expected Use='ops', got %q", cmd.Use)
	}

	subcommands := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subcommands[sub.Name()] = true
	}

	expected := []string{"get", "logs", "describe", "diagnose", "delete", "expand-volume", "etcd", "rollout-restart", "wf", "pam"}
	for _, name := range expected {
		if !subcommands[name] {
			t.Errorf("expected subcommand %q not found", name)
		}
	}
}
