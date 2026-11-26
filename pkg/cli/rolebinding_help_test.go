package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRoleBindingConditionHelp(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "help must not contact the API", http.StatusInternalServerError)
	}))
	defer server.Close()
	// Help must bypass configuration loading and must not invoke gcloud/ADC.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing.json"))
	configPath := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(configPath, []byte("output: [invalid yaml"), 0600); err != nil {
		t.Fatal(err)
	}

	shared := make(map[string]string)
	for _, command := range []string{"create", "update"} {
		t.Run(command, func(t *testing.T) {
			root := newRootCmd()
			var out, stderr bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&stderr)
			root.SetArgs([]string{"rolebinding", command, "--help", "--config", configPath, "--api-endpoint", server.URL})
			if err := root.Execute(); err != nil || stderr.Len() != 0 {
				t.Fatalf("help error=%v stderr=%q", err, stderr.String())
			}
			help := out.String()
			last := -1
			for _, section := range []string{"Usage:", "Examples:", "Flags:", "Global Flags:", "Conditions:", "Safety:"} {
				index := strings.Index(help, "\n"+section+"\n")
				if index <= last {
					t.Fatalf("missing or out-of-order section %q:\n%s", section, help)
				}
				last = index
			}
			opening := strings.TrimSpace(help[:strings.Index(help, "\nUsage:\n")])
			if strings.Contains(opening, "\n\n") || strings.Contains(opening, "--condition ''") || strings.Contains(opening, "Cedar boolean expression") {
				t.Fatalf("opening should be one command-focused paragraph, not condition guidance: %q", opening)
			}
			for _, want := range []string{
				fmt.Sprintf("gcphcpctl rolebinding %s <name> [flags]", command),
				"--condition stringArray", "--subject string", "--platform-role string", "--role string",
				"nonempty JSON string", "Only In and NotIn are supported", "replace the complete list", "sole wildcard",
				"Independent grants remain additive", "Changes may take time to propagate",
				"Keep a separate administrator grant",
			} {
				if !strings.Contains(help, want) {
					t.Errorf("help missing %q:\n%s", want, help)
				}
			}
			examples := help[strings.Index(help, "\nExamples:\n"):strings.Index(help, "\nFlags:\n")]
			if command == "create" {
				if !strings.HasPrefix(opening, "Create a RoleBinding") || !strings.Contains(examples, "rolebinding create regional-viewer") {
					t.Fatalf("missing create-specific description/examples:\n%s", help)
				}
			} else {
				for _, want := range []string{
					"Only supplied fields change", "At least one update flag is required", "permission-set replacement",
					"--subject bob@example.com", "--clear-conditions", "Replace the complete list",
				} {
					if !strings.Contains(help, want) {
						t.Errorf("update help missing %q:\n%s", want, help)
					}
				}
			}
			if strings.Count(help, "\nConditions:\n") != 1 || strings.Count(help, "\nSafety:\n") != 1 {
				t.Fatal("shared guidance should appear exactly once")
			}
			shared[command] = help[strings.Index(help, "\nConditions:\n"):]
		})
	}
	if len(shared) != 2 || shared["create"] != shared["update"] {
		t.Fatal("create and update must display identical shared condition guidance")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("help made %d API requests", got)
	}
}

func TestConditionHelpIsCommandScoped(t *testing.T) {
	for _, args := range [][]string{{"rolebinding", "get", "--help"}, {"role", "create", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := newRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "\nConditions:\n") || strings.Contains(out.String(), "\nSafety:\n") {
				t.Fatal("condition help leaked into another command")
			}
		})
	}
}

// Real root execution must reject invalid requirements before inherited setup.
func TestRoleBindingConditionsBeforeDiscoveryAndAuth(t *testing.T) {
	var calls atomic.Int32
	old := http.DefaultTransport
	http.DefaultTransport = platformRoleTraceTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, fmt.Errorf("unexpected network request")
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	dir := t.TempDir()
	marker := filepath.Join(dir, "auth-invoked")
	if err := os.WriteFile(filepath.Join(dir, "gcloud"), []byte("#!/bin/sh\nprintf invoked > '"+marker+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(dir, "missing.json"))
	t.Setenv("GCPHCPCTL_API_ENDPOINT", "")
	t.Setenv("GCPHCPCTL_REGION", "us-central1")
	t.Setenv("GCPHCPCTL_ENVIRONMENT", "development")
	t.Setenv("GCPHCPCTL_PROJECT", "customer")
	for _, verb := range []string{"create", "update"} {
		for _, flags := range [][]string{{"--condition", `name == "x"`}, {"--condition", `name != "x"`}, {"--condition", ""}, {"--condition", `name In [null]`}, {"--condition", `name In ["x"]`, "--condition", "broken"}, {"--condition", `name In ["x"]`, "--clear-conditions=false"}} {
			root := newRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			args := append([]string{"rolebinding", verb, "grant", "--config", filepath.Join(dir, "absent.yaml")}, flags...)
			root.SetArgs(args)
			err := root.Execute()
			if err == nil || (!strings.Contains(err.Error(), "condition") && !strings.Contains(err.Error(), "unknown flag")) {
				t.Fatalf("%v: %v", args, err)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("%d network calls", calls.Load())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("auth invoked")
	}
}
