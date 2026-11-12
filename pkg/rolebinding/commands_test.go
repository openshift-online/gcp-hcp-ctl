package rolebinding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/internal/access"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

const testBase = "/apis/gcp.managed.openshift.io/v1/namespaces/customer/rolebindings"
const propagation = "Note: authorization changes may take a moment to propagate globally."

func executeBinding(t *testing.T, handler http.HandlerFunc, args ...string) (string, string, error) {
	t.Helper()
	server := httptest.NewServer(handler)
	defer server.Close()
	return executeBindingAt(t, server.URL, args...)
}

// executeBindingAt lets sequential commands share one stateful HTTP fixture.
func executeBindingAt(t *testing.T, serverURL string, args ...string) (string, string, error) {
	t.Helper()
	client, err := platformapi.NewClientForTest(serverURL, "customer")
	if err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "test", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("project", "customer", "")
	root.PersistentFlags().String("api-endpoint", serverURL, "")
	root.PersistentFlags().StringP("output", "o", "text", "")
	root.AddCommand(NewRoleBindingCmd())
	var out, stderr bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"rolebinding"}, args...))
	err = root.ExecuteContext(access.WithClient(context.Background(), client))
	return out.String(), stderr.String(), err
}
func obj(spec map[string]any) map[string]any {
	return map[string]any{"apiVersion": "gcp.managed.openshift.io/v1", "kind": "RoleBinding", "metadata": map[string]any{"name": "grant", "namespace": "customer", "uid": "original", "labels": map[string]string{"keep": "yes"}}, "spec": spec}
}
func response(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func TestCreateConditionWire(t *testing.T) {
	cases := []struct {
		name      string
		flags     []string
		condition *string
	}{
		{"absent", nil, nil}, {"empty", []string{"--condition", ""}, str("")}, {"equals", []string{"--condition="}, str("")},
		{"whitespace", []string{"--condition", "  \n\t"}, str("  \n\t")}, {"complex", []string{"--condition", "a == \"é\"\n&& b"}, str("a == \"é\"\n&& b")}}
	for _, tc := range cases {
		for _, ref := range []string{"role", "platform-role"} {
			t.Run(tc.name+ref, func(t *testing.T) {
				calls := 0
				args := append([]string{"create", "grant", "--subject", " Alice@EXAMPLE.COM ", "--" + ref, "viewer", "-o", "json"}, tc.flags...)
				out, stderr, err := executeBinding(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != "POST" || r.URL.Path != testBase {
						t.Errorf("%s %s", r.Method, r.URL.Path)
					}
					var body map[string]any
					if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
						t.Fatal(e)
					}
					spec := body["spec"].(map[string]any)
					value, ok := spec["condition"]
					if ok != (tc.condition != nil) || ok && value != *tc.condition {
						t.Errorf("condition %#v", spec)
					}
					if spec["subject"] != "Alice@example.com" {
						t.Error(spec)
					}
					response(w, body)
				}, args...)
				if err != nil || calls != 1 || strings.Count(stderr, propagation) != 1 {
					t.Fatalf("%v %d %s %s", err, calls, out, stderr)
				}
			})
		}
	}
}
func str(s string) *string { return &s }
func TestUpdatePatch(t *testing.T) {
	cases := []struct {
		name     string
		flags    []string
		expected map[string]any
	}{
		{"subject", []string{"--subject", " Bob@EXAMPLE.COM "}, map[string]any{"subject": "Bob@example.com"}},
		{"custom", []string{"--role", "custom"}, map[string]any{"roleRef": map[string]any{"kind": "Role", "name": "custom", "apiGroup": "gcp.managed.openshift.io"}}},
		{"platform", []string{"--platform-role", "viewer"}, map[string]any{"roleRef": map[string]any{"kind": "PlatformRole", "name": "viewer", "apiGroup": "gcp.managed.openshift.io"}}},
		{"condition", []string{"--condition", " \ntrue "}, map[string]any{"condition": " \ntrue "}},
		{"clear", []string{"--condition="}, map[string]any{"condition": ""}},
		{"subject-condition", []string{"--subject", "b@X", "--condition", ""}, map[string]any{"subject": "b@x", "condition": ""}},
		{"role-condition", []string{"--role", "x", "--condition", "c"}, map[string]any{"roleRef": map[string]any{"kind": "Role", "name": "x", "apiGroup": "gcp.managed.openshift.io"}, "condition": "c"}},
		{"subject-role", []string{"--subject", "b@X", "--role", "x"}, map[string]any{"subject": "b@x", "roleRef": map[string]any{"kind": "Role", "name": "x", "apiGroup": "gcp.managed.openshift.io"}}},
		{"all", []string{"--subject", "b@X", "--platform-role", "x", "--condition", ""}, map[string]any{"subject": "b@x", "condition": "", "roleRef": map[string]any{"kind": "PlatformRole", "name": "x", "apiGroup": "gcp.managed.openshift.io"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			original := map[string]any{"subject": "old@example.com", "roleRef": map[string]any{"kind": "Role", "name": "old", "apiGroup": "gcp.managed.openshift.io"}, "condition": "old condition"}
			args := append([]string{"update", "grant", "-o", "json"}, tc.flags...)
			out, stderr, err := executeBinding(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PATCH" || r.URL.Path != testBase+"/grant" || r.Header.Get("Content-Type") != "application/merge-patch+json" {
					t.Errorf("%s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
				}
				var body map[string]any
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					t.Fatal(e)
				}
				if len(body) != 1 || !reflect.DeepEqual(body["spec"], tc.expected) {
					t.Errorf("patch %#v want %#v", body, tc.expected)
				}
				for k, v := range tc.expected {
					original[k] = v
				}
				response(w, obj(original))
			}, args...)
			if err != nil || calls != 1 || strings.Count(stderr, propagation) != 1 {
				t.Fatalf("%v %d %s %s", err, calls, out, stderr)
			}
			var got map[string]any
			if e := json.Unmarshal([]byte(out), &got); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(got["spec"], original) || got["metadata"].(map[string]any)["uid"] != "original" {
				t.Fatal(got)
			}
		})
	}
}
func TestUpdateValidationNoRequests(t *testing.T) {
	for _, args := range [][]string{{"update"}, {"update", " "}, {"update", "grant"}, {"update", "grant", "--subject", ""}, {"update", "grant", "--subject", "bad"}, {"update", "grant", "--role", " "}, {"update", "grant", "--platform-role", ""}, {"update", "grant", "--role", "a", "--platform-role", "b"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			calls := 0
			out, errout, err := executeBinding(t, func(http.ResponseWriter, *http.Request) { calls++ }, args...)
			if err == nil || calls != 0 || strings.Contains(out+errout, propagation) {
				t.Fatalf("%v calls %d %s %s", err, calls, out, errout)
			}
		})
	}
}
func TestUpdateErrorsAreSafe(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, "invalid request"},
		{http.StatusUnauthorized, "not authenticated"},
		{http.StatusForbidden, "permission denied"},
		{http.StatusNotFound, "not found"},
		{http.StatusConflict, "conflict"},
		{http.StatusUnprocessableEntity, "invalid request"},
		{http.StatusInternalServerError, "server error"},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			calls := 0
			out, stderr, err := executeBinding(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"message":"SECRET SERVER PROSE"}`)
			}, "update", "grant", "--condition", "invalid")
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			var httpErr *platformapi.HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode() != tc.status || httpErr.Method() != http.MethodPatch || httpErr.Resource() != "rolebindings" || httpErr.Name() != "grant" {
				t.Fatalf("HTTP error metadata = %#v", httpErr)
			}
			if calls != 1 || out != "" || stderr != "" {
				t.Fatalf("calls=%d, stdout=%q, stderr=%q; want one request and no success output", calls, out, stderr)
			}
		})
	}
}
func TestGetAndListFormats(t *testing.T) {
	condition := "line one\n\t'long condition'"
	for _, format := range []string{"text", "json", "yaml"} {
		for _, command := range []string{"get", "list"} {
			t.Run(format+command, func(t *testing.T) {
				args := []string{command}
				if command == "get" {
					args = append(args, "grant")
				}
				args = append(args, "-o", format)
				out, stderr, err := executeBinding(t, func(w http.ResponseWriter, r *http.Request) {
					binding := obj(map[string]any{"subject": "a@b", "roleRef": map[string]any{"kind": "Role", "name": "r", "apiGroup": "gcp.managed.openshift.io"}, "condition": condition})
					if command == "list" {
						response(w, map[string]any{"apiVersion": "gcp.managed.openshift.io/v1", "kind": "RoleBindingList", "metadata": map[string]any{"resourceVersion": "5"}, "items": []any{binding}})
					} else {
						response(w, binding)
					}
				}, args...)
				if err != nil || stderr != "" {
					t.Fatalf("%v %s", err, stderr)
				}
				if format == "text" {
					if command == "get" && !strings.Contains(out, "--condition "+conditionArgument(&condition)) {
						t.Fatal(out)
					}
					if command == "list" && (!strings.Contains(out, `line one\n\t`) || strings.Contains(out, condition)) {
						t.Fatal(out)
					}
				} else {
					var v map[string]any
					if e := yaml.Unmarshal([]byte(out), &v); e != nil {
						t.Fatal(e)
					}
					b := v
					if command == "list" {
						b = v["items"].([]any)[0].(map[string]any)
					}
					if b["spec"].(map[string]any)["condition"] != condition || b["metadata"].(map[string]any)["uid"] != "original" {
						t.Fatal(v)
					}
				}
			})
		}
	}
}

// The fixture rejects supplied expressions; this tests CLI pass-through and safe
// error handling, not Cedar validity or the live server's length constraints.
func TestConditionsForwardedToServerAndRejectedSafely(t *testing.T) {
	for _, command := range []string{"create", "update"} {
		for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
			for _, tc := range []struct{ name, expression string }{
				{"invalid-syntax", "this is not Cedar"},
				{"4095-runes", strings.Repeat("é", 4095)},
				{"4096-runes", strings.Repeat("é", 4096)},
				{"4097-runes", strings.Repeat("é", 4097)},
				{"namespace-reference", `principal == Namespace::"x"`},
				{"trailing-statement", `true } permit(principal, action, resource) when { true`},
			} {
				t.Run(fmt.Sprintf("%s/%d/%s", command, status, tc.name), func(t *testing.T) {
					args := []string{command, "grant", "--condition", tc.expression, "-o", "json"}
					method, path := http.MethodPatch, testBase+"/grant"
					if command == "create" {
						args = append(args, "--subject", "a@b", "--role", "viewer")
						method, path = http.MethodPost, testBase
					}
					calls := 0
					out, stderr, err := executeBinding(t, func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.Method != method || r.URL.Path != path {
							t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, method, path)
						}
						var body struct {
							Spec map[string]any `json:"spec"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							http.Error(w, "invalid fixture request", http.StatusBadRequest)
							return
						}
						if body.Spec["condition"] != tc.expression {
							t.Error("condition not sent unchanged")
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(status)
						if _, err := fmt.Fprint(w, `{"message":"SECRET invalid policy"}`); err != nil {
							t.Errorf("writing rejection fixture response: %v", err)
						}
					}, args...)
					if err == nil || err.Error() != "invalid request" || calls != 1 || out != "" || stderr != "" {
						t.Fatalf("error=%v calls=%d stdout=%q stderr=%q", err, calls, out, stderr)
					}
				})
			}
		}
	}
}

func TestConditionUpdateDanglingRoleAndMalformedResponse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{{"dangling", 400, `{"message":"deleted role private detail"}`}, {"malformed", 200, `{"kind":"RoleBinding","spec":`}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			out, stderr, err := executeBinding(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}, "update", "grant", "--condition", "")
			if err == nil || calls != 1 || strings.Contains(err.Error(), "private") || strings.Contains(out+stderr, propagation) {
				t.Fatalf("%v %d %q %q", err, calls, out, stderr)
			}
		})
	}
}

func TestConditionStatesAndPreservation(t *testing.T) {
	for _, initial := range []struct {
		name      string
		condition *string
	}{{"absent", nil}, {"empty", str("")}, {"present", str("a\n b")}} {
		t.Run(initial.name, func(t *testing.T) {
			spec := map[string]any{"subject": "old@x", "roleRef": map[string]any{"kind": "Role", "name": "old", "apiGroup": "gcp.managed.openshift.io"}}
			if initial.condition != nil {
				spec["condition"] = *initial.condition
			}
			out, stderr, err := executeBinding(t, func(w http.ResponseWriter, r *http.Request) {
				var patch map[string]any
				_ = json.NewDecoder(r.Body).Decode(&patch)
				fields := patch["spec"].(map[string]any)
				for k, v := range fields {
					spec[k] = v
				}
				response(w, obj(spec))
			}, "update", "grant", "--subject", "new@x", "-o", "yaml")
			if err != nil || strings.Count(stderr, propagation) != 1 {
				t.Fatalf("%v %q %q", err, out, stderr)
			}
			var got map[string]any
			if e := yaml.Unmarshal([]byte(out), &got); e != nil {
				t.Fatal(e)
			}
			value, ok := got["spec"].(map[string]any)["condition"]
			if ok != (initial.condition != nil) || ok && value != *initial.condition {
				t.Fatalf("condition=%#v present=%v", value, ok)
			}
		})
	}
}

func TestListConditionPagination(t *testing.T) {
	calls := 0
	out, stderr, err := executeBinding(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		items := []any{}
		meta := map[string]any{"resourceVersion": "5"}
		switch r.URL.Query().Get("continue") {
		case "":
			meta["continue"] = "next"
			items = append(items, obj(map[string]any{"subject": "alice@example.com", "roleRef": map[string]any{"kind": "Role", "name": "a"}, "condition": "first"}))
		case "next":
			items = append(items, obj(map[string]any{"subject": "alice@example.com", "roleRef": map[string]any{"kind": "Role", "name": "b"}, "condition": "second"}), obj(map[string]any{"subject": "bob@example.com", "roleRef": map[string]any{"kind": "Role", "name": "c"}, "condition": "ignored"}))
		default:
			t.Error("bad token")
		}
		response(w, map[string]any{"apiVersion": "gcp.managed.openshift.io/v1", "kind": "RoleBindingList", "metadata": meta, "items": items})
	}, "list", "--subject", "alice@EXAMPLE.COM", "-o", "json")
	if err != nil || calls != 2 || stderr != "" {
		t.Fatalf("%v %d %q", err, calls, stderr)
	}
	var list map[string]any
	if e := json.Unmarshal([]byte(out), &list); e != nil {
		t.Fatal(e)
	}
	items := list["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["spec"].(map[string]any)["condition"] != "first" || items[1].(map[string]any)["spec"].(map[string]any)["condition"] != "second" || list["metadata"].(map[string]any)["resourceVersion"] != "5" {
		t.Fatal(list)
	}
}

func TestMutationOutputUsesReturnedCondition(t *testing.T) {
	for _, command := range []string{"create", "update"} {
		for _, format := range []string{"text", "json", "yaml"} {
			t.Run(command+format, func(t *testing.T) {
				actual := "server value\n  'quoted'"
				args := []string{command, "grant", "--condition", "client value", "-o", format}
				if command == "create" {
					args = append(args, "--subject", "a@b", "--role", "r")
				}
				out, stderr, err := executeBinding(t, func(w http.ResponseWriter, r *http.Request) {
					response(w, obj(map[string]any{"subject": "a@b", "roleRef": map[string]any{"kind": "Role", "name": "r", "apiGroup": "gcp.managed.openshift.io"}, "condition": actual}))
				}, args...)
				if err != nil || strings.Count(out+stderr, propagation) != 1 {
					t.Fatalf("%v %q %q", err, out, stderr)
				}
				if format == "text" {
					if stderr != "" || !strings.Contains(out, "Condition:\n--condition "+conditionArgument(&actual)+"\n") || strings.Contains(out, "client value") {
						t.Fatalf("%q %q", out, stderr)
					}
					return
				}
				if strings.Contains(out, propagation) || !strings.Contains(stderr, propagation) {
					t.Fatalf("%q %q", out, stderr)
				}
				var got map[string]any
				if e := yaml.Unmarshal([]byte(out), &got); e != nil {
					t.Fatal(e)
				}
				if got["spec"].(map[string]any)["condition"] != actual {
					t.Fatal(got)
				}
			})
		}
	}
}

func TestPatchTransportFailure(t *testing.T) {
	calls := 0
	out, stderr, err := executeBinding(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		conn, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		_ = conn.Close()
	}, "update", "grant", "--condition", "true")
	if err == nil || calls != 1 || strings.Contains(out+stderr, propagation) {
		t.Fatalf("%v calls=%d %q %q", err, calls, out, stderr)
	}
}
