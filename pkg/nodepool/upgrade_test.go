package nodepool

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
)

func TestUpgradeCommandPatch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		start      any
		jsonOutput bool
	}{
		{name: "immediate clears pending schedule", args: []string{"workers", "--version", "4.22.16"}},
		{name: "structured response", args: []string{"workers", "--version", "4.22.16", "-o", "json"}, jsonOutput: true},
		{name: "candidate release", args: []string{"workers", "--version", "5.1.0-ec.2"}},
		{name: "scheduled normalizes time", args: []string{"workers", "--version", "4.22.16", "--start-time", "2026-11-06T04:00:00+01:00"}, start: "2026-11-06T03:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPatch || r.URL.Path != "/apis/gcp.managed.openshift.io/v1/namespaces/my-project/nodepools/workers" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Content-Type") != "application/merge-patch+json" {
					t.Errorf("unexpected patch type: %s", r.Header.Get("Content-Type"))
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				spec := body["spec"].(map[string]any)
				release := spec["release"].(map[string]any)
				if len(body) != 1 || len(spec) != 1 || len(release) != 2 || release["version"] != tc.args[2] {
					t.Errorf("unexpected patch: %#v", body)
				}
				start, present := release["upgradeStartTime"]
				if !present || start != tc.start {
					t.Errorf("upgradeStartTime = %#v, want %#v", start, tc.start)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "gcp.managed.openshift.io/v1", "kind": "NodePool", "metadata": map[string]any{"name": "workers"}, "spec": map[string]any{"release": release}, "status": map[string]any{"version": "4.22.14"}})
			}))
			defer server.Close()
			client, err := platformapi.NewClientForTest(server.URL, "my-project")
			if err != nil {
				t.Fatal(err)
			}
			cmd := newUpgradeCmd()
			cmd.SetContext(context.WithValue(t.Context(), clientKey, client))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d, want one PATCH", calls)
			}
			if tc.jsonOutput {
				var pool gcpv1.NodePool
				if err := json.Unmarshal(out.Bytes(), &pool); err != nil {
					t.Fatal(err)
				}
				if pool.Name != "workers" || pool.Spec.Release.Version != tc.args[2] || pool.Status.Version != "4.22.14" {
					t.Fatalf("unexpected response: %#v", pool)
				}
			} else if want := "Nodepool upgrade requested for workers (target " + tc.args[2] + ").\n"; out.String() != want {
				t.Errorf("output=%q, want %q", out.String(), want)
			}
		})
	}
}

func TestUpgradeCommandInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{}, {"workers"}, {"workers", "--version", "invalid"}, {"workers", "--version", "4.22"}, {"workers", "--version", "v4.22.16"}, {"workers", "--version", "4.22.16+build"}, {"workers", "--version", "4.22.16", "--start-time", "tomorrow"}, {"workers", "extra", "--version", "4.22.16"},
	} {
		cmd := newUpgradeCmd()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("expected error for %v", args)
		}
	}
}

func TestUpgradeCommandDoesNotLeakAPIError(t *testing.T) {
	err, out, stderr := executeNodepoolError(t, 403, `{"error":"Bearer synthetic-token alice@example.invalid"}`, "upgrade", "workers", "--version", "4.22.16")
	if err == nil || err.Error() != "requesting nodepool upgrade: permission denied" || out != "" {
		t.Fatalf("error=%v, stdout=%q", err, out)
	}
	if strings.Contains(stderr, "synthetic-token") || strings.Contains(stderr, "alice@example.invalid") {
		t.Fatalf("server error leaked: %s", stderr)
	}
}

func TestUpgradeCommandReportsAdmissionRejection(t *testing.T) {
	err, out, _ := executeNodepoolError(t, http.StatusUnprocessableEntity,
		`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Invalid","message":"release settings are locked during an upgrade"}`,
		"upgrade", "workers", "--version", "4.22.17")
	if err == nil || err.Error() != "requesting nodepool upgrade: invalid request" || out != "" {
		t.Fatalf("error=%v, stdout=%q", err, out)
	}
}
