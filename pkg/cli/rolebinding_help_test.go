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
				`context.resourceName like "bob*"`,
				"--condition string", "--subject string", "--platform-role string", "--role string",
				"Cedar boolean expression, not a complete policy", `"has" before accessing optional fields`,
				"Omitting --condition creates an unconditional binding on create",
				"preserves the existing condition on update", "(--condition '') makes the binding unconditional",
				"context.resourcePlural", "context.resourceName", "context.method", "context.spec",
				"https://docs.cedarpolicy.com/policies/syntax-operators.html",
				"independent grants may still", "Changes may take time to propagate",
				"Keep a separate administrator grant", "prevent you from managing access or clearing it",
			} {
				if !strings.Contains(help, want) {
					t.Errorf("help missing %q:\n%s", want, help)
				}
			}
			examples := help[strings.Index(help, "\nExamples:\n"):strings.Index(help, "\nFlags:\n")]
			if command == "create" {
				if !strings.HasPrefix(opening, "Create a RoleBinding") || !strings.Contains(examples, "rolebinding create bob-viewer") || !strings.Contains(examples, "rolebinding create viewer") {
					t.Fatalf("missing create-specific description/examples:\n%s", help)
				}
			} else {
				for _, want := range []string{
					"Only supplied fields change", "At least one update flag is required", "permission-set replacement",
					"--subject bob@example.com", "--role limited-operations", "--platform-role cluster-viewer",
					"# Replace the condition.", "# Clear the condition.", "# Change several fields together.",
					"--subject bob@example.com --role limited-operations --condition ''",
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
