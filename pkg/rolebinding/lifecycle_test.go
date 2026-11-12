package rolebinding

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// This fixture models persistence across commands, not Cedar enforcement. Exact
// payload assertions independently check that omitted fields never reach PATCH.
func TestUpdateConditionLifecycleAndPlatformRoleToRole(t *testing.T) {
	stored := obj(map[string]any{
		"subject": "alice@example.com",
		"roleRef": map[string]any{
			"kind": "PlatformRole", "name": "cluster-viewer", "apiGroup": "gcp.managed.openshift.io",
		},
		"condition": `context.resourceName == "old-cluster"`,
	})
	stored["metadata"].(map[string]any)["annotations"] = map[string]string{"keep": "annotation"}
	// Round-trip once so expected nested maps have the same JSON representation
	// as command output, and do not alias the fixture's persisted state.
	initial, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(initial, &want); err != nil {
		t.Fatal(err)
	}

	type request struct {
		method string
		body   map[string]any
	}
	var mu sync.Mutex
	var requests []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != testBase+"/grant" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodPatch:
			if got := r.Header.Get("Content-Type"); got != "application/merge-patch+json" {
				t.Errorf("Content-Type = %q", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				http.Error(w, "invalid fixture request", http.StatusBadRequest)
				return
			}
			requests = append(requests, request{method: r.Method, body: body})
			fields, ok := body["spec"].(map[string]any)
			if !ok {
				t.Error("PATCH has no spec object")
				http.Error(w, "invalid fixture request", http.StatusBadRequest)
				return
			}
			for key, value := range fields {
				stored["spec"].(map[string]any)[key] = value
			}
		case http.MethodGet:
			requests = append(requests, request{method: r.Method})
		default:
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		response(w, stored)
	}))
	defer server.Close()

	assertOutput := func(t *testing.T, out string) {
		t.Helper()
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("returned binding = %#v, want %#v (including UID, metadata, and omitted fields)", got, want)
		}
	}
	for i, step := range []struct {
		name  string
		flags []string
		patch map[string]any
	}{
		{
			name:  "replace-condition",
			flags: []string{"--condition", "  context.resourceName like \"greg*\" &&\n  true  "},
			patch: map[string]any{"condition": "  context.resourceName like \"greg*\" &&\n  true  "},
		},
		{
			name:  "clear-condition",
			flags: []string{"--condition", ""},
			patch: map[string]any{"condition": ""},
		},
		{
			name:  "platform-role-to-custom-role",
			flags: []string{"--role", "custom-reader"},
			patch: map[string]any{"roleRef": map[string]any{
				"kind": "Role", "name": "custom-reader", "apiGroup": "gcp.managed.openshift.io",
			}},
		},
	} {
		// Keep the dependent transitions in one test: selecting a subtest must
		// not accidentally skip the state needed by a later transition.
		t.Logf("step %d: %s", i+1, step.name)
		for key, value := range step.patch {
			want["spec"].(map[string]any)[key] = value
		}
		args := append([]string{"update", "grant", "-o", "json"}, step.flags...)
		out, stderr, err := executeBindingAt(t, server.URL, args...)
		if err != nil || stderr != propagation+"\n" {
			t.Fatalf("update error=%v stdout=%q stderr=%q", err, out, stderr)
		}
		assertOutput(t, out)
		mu.Lock()
		mutationRequests := append([]request(nil), requests...)
		mu.Unlock()
		if len(mutationRequests) != 2*i+1 {
			t.Fatalf("requests after update = %#v; want exactly one PATCH, no preliminary GET or retry", mutationRequests)
		}
		last := mutationRequests[len(mutationRequests)-1]
		if last.method != http.MethodPatch || !reflect.DeepEqual(last.body, map[string]any{"spec": step.patch}) {
			t.Fatalf("mutation request = %#v, want only supplied spec fields %#v", last, step.patch)
		}

		out, stderr, err = executeBindingAt(t, server.URL, "get", "grant", "-o", "json")
		if err != nil || stderr != "" || strings.Contains(out, propagation) {
			t.Fatalf("read-back error=%v stdout=%q stderr=%q", err, out, stderr)
		}
		assertOutput(t, out)
		mu.Lock()
		readRequests := append([]request(nil), requests...)
		mu.Unlock()
		if len(readRequests) != 2*(i+1) || readRequests[len(readRequests)-1].method != http.MethodGet {
			t.Fatalf("requests after read-back = %#v, want one independent GET", readRequests)
		}
	}
}
