package ops

import (
	"strings"
	"testing"
)

func TestParseEtcdOutput(t *testing.T) {
	tests := []struct {
		name     string
		output   interface{}
		wantType string
	}{
		{"JSON array string", `[{"health":true}]`, "[]interface {}"},
		{"JSON object string", `{"members":[]}`, "map[string]interface {}"},
		{"already parsed slice", []interface{}{map[string]interface{}{"health": true}}, "[]interface {}"},
		{"plain text", "Finished defragmenting", "string"},
		{"invalid JSON", `[{"broken`, "string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := map[string]interface{}{"output": tt.output}
			parsed := parseEtcdOutput(result)
			got := typeString(parsed)
			if got != tt.wantType {
				t.Errorf("got type %s, want %s", got, tt.wantType)
			}
		})
	}

	t.Run("no output key", func(t *testing.T) {
		result := map[string]interface{}{"status": "success"}
		parsed := parseEtcdOutput(result)
		if _, ok := parsed.(map[string]interface{}); !ok {
			t.Errorf("expected map fallback, got %T", parsed)
		}
	})
}

func typeString(v interface{}) string {
	switch v.(type) {
	case []interface{}:
		return "[]interface {}"
	case map[string]interface{}:
		return "map[string]interface {}"
	case string:
		return "string"
	default:
		return "unknown"
	}
}

func TestCleanEtcdError(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:            "strips RuntimeError wrapper and JSON log lines",
			input:           `RuntimeError: "{\"level\":\"warn\",\"msg\":\"failed\"}\nError: unhealthy cluster\n"`,
			wantContains:    []string{"Error: unhealthy cluster"},
			wantNotContains: []string{"RuntimeError", "retrying"},
		},
		{
			name:            "strips in step trailer",
			input:           "some error\nin step \"raise_error_result\", routine \"main\", line: 301",
			wantContains:    []string{"some error"},
			wantNotContains: []string{"in step"},
		},
		{
			name:            "filters multiple JSON log lines, keeps text lines",
			input:           `RuntimeError: "{\"level\":\"warn\"}\nFailed on etcd-0\nFinished on etcd-1\n{\"level\":\"error\"}\nFailed on etcd-2\n"`,
			wantContains:    []string{"Failed on etcd-0", "Finished on etcd-1", "Failed on etcd-2"},
			wantNotContains: []string{"level"},
		},
		{
			name:         "passes through plain error",
			input:        "something went wrong",
			wantContains: []string{"something went wrong"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanEtcdError(tt.input)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("expected %q in output, got:\n%s", want, got)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(got, notWant) {
					t.Errorf("unexpected %q in output, got:\n%s", notWant, got)
				}
			}
		})
	}
}

func TestParseJSONFromError(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
		wantLen int
	}{
		{
			name:    "extracts JSON array from mixed content",
			input:   `{\"level\":\"warn\"}\n[{\"health\":true},{\"health\":false}]\nError: unhealthy\n`,
			wantLen: 2,
		},
		{
			name:    "handles escaped quotes",
			input:   `[{\"endpoint\":\"https://etcd-0:2379\",\"health\":true}]`,
			wantLen: 1,
		},
		{
			name:    "returns nil when no array",
			input:   `{\"level\":\"warn\"}\nError: timeout\n`,
			wantNil: true,
		},
		{
			name:    "returns nil for plain text",
			input:   "some error without JSON",
			wantNil: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseJSONFromError(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %T", got)
				}
				return
			}
			arr, ok := got.([]interface{})
			if !ok {
				t.Fatalf("expected []interface{}, got %T", got)
			}
			if len(arr) != tt.wantLen {
				t.Errorf("expected %d items, got %d", tt.wantLen, len(arr))
			}
		})
	}
}

func TestNewEtcdCmd(t *testing.T) {
	cmd := newEtcdCmd()
	subcommands := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subcommands[sub.Name()] = true
	}
	for _, name := range []string{"health", "status", "member-list", "defrag", "compact"} {
		if !subcommands[name] {
			t.Errorf("expected etcd subcommand %q not found", name)
		}
	}
}
