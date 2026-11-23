package wifconfigtemplate

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/internal/access"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

const templatePath = "/apis/gcp.managed.openshift.io/v1/wifconfigtemplates"

func runTemplateCommand(t *testing.T, client *platformapi.Client, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "gcphcpctl", SilenceUsage: true, SilenceErrors: true}
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.PersistentFlags().String("project", "test-project", "")
	root.PersistentFlags().String("api-endpoint", "https://api.example.test", "")
	root.PersistentFlags().String("region", "test-region", "")
	root.PersistentFlags().String("environment", "", "")
	root.PersistentFlags().StringP("output", "o", "text", "")
	root.AddCommand(NewWIFConfigTemplateCmd())
	root.SetContext(access.WithClient(t.Context(), client))
	root.SetArgs(append([]string{"wifconfigtemplate"}, args...))
	err := root.Execute()
	return out.String(), err
}

func templateClient(t *testing.T, handler http.HandlerFunc) *platformapi.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := platformapi.NewClientForTest(server.URL, "test-project")
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestGetTemplateOutputAndReadOnly(t *testing.T) {
	client := templateClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != templatePath+"/5.0.0" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"kind":"WIFConfigTemplate","apiVersion":"gcp.managed.openshift.io/v1","metadata":{"name":"5.0.0","uid":"template-uid","managedFields":[{"manager":"test"}]},"spec":{"schemaVersion":1,"verified":false,"iam":{"requiredComponents":["cloud-controller"]}}}`)
	})
	for _, format := range []string{"text", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			out, err := runTemplateCommand(t, client, "get", "5.0.0", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "managedFields") {
				t.Fatalf("managedFields leaked: %s", out)
			}
			switch format {
			case "text":
				for _, want := range []string{"5.0.0", "template-uid", "false", "sha256:", "-o yaml"} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q: %s", want, out)
					}
				}
			default:
				var got gcpv1.WIFConfigTemplate
				if err := yaml.Unmarshal([]byte(out), &got); err != nil {
					t.Fatal(err)
				}
				if got.Kind != "WIFConfigTemplate" || got.Spec.Verified == nil || *got.Spec.Verified || got.Spec.IAM.RequiredComponents[0] != "cloud-controller" {
					t.Errorf("incomplete contract: %+v", got)
				}
			}
		})
	}
}

func TestListTemplateOutputAndPagination(t *testing.T) {
	client := templateClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != templatePath {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("continue") == "" {
			fmt.Fprint(w, `{"kind":"WIFConfigTemplateList","apiVersion":"gcp.managed.openshift.io/v1","metadata":{"continue":"next"},"items":[{"metadata":{"name":"5.0.10"},"spec":{"schemaVersion":1,"verified":true}},{"metadata":{"name":"5.0.9"},"spec":{"schemaVersion":1,"verified":false}}]}`)
		} else if r.URL.Query().Get("continue") == "next" {
			fmt.Fprint(w, `{"kind":"WIFConfigTemplateList","apiVersion":"gcp.managed.openshift.io/v1","items":[{"metadata":{"name":"5.0.0"},"spec":{"schemaVersion":1,"verified":false}}]}`)
		} else {
			t.Errorf("unexpected pagination token: %s", r.URL.RawQuery)
		}
	})
	for _, format := range []string{"text", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			out, err := runTemplateCommand(t, client, "list", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Index(out, "5.0.9") > strings.Index(out, "5.0.10") || strings.Index(out, "5.0.0") > strings.Index(out, "5.0.9") {
				t.Errorf("not sorted: %s", out)
			}
			if format == "text" {
				if !strings.Contains(out, "CONTRACT DIGEST") || strings.Count(out, "sha256:") != 3 || !strings.Contains(out, "false") || !strings.Contains(out, "true") {
					t.Errorf("missing table columns: %s", out)
				}
				return
			}
			var got gcpv1.WIFConfigTemplateList
			if err := yaml.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			if got.Kind != "WIFConfigTemplateList" || len(got.Items) != 3 || got.Items[0].Name != "5.0.0" {
				t.Errorf("list missing items: %+v", got)
			}
		})
	}
}

func TestTemplateDigestExcludesVerification(t *testing.T) {
	spec := gcpv1.WIFConfigTemplateSpec{SchemaVersion: 1}
	falseValue, trueValue := false, true
	spec.Verified = &falseValue
	first, err := application.Digest(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Verified = &trueValue
	second, err := application.Digest(spec)
	if err != nil || first != second {
		t.Fatalf("digest changed on verification: %s %s %v", first, second, err)
	}
}

func TestTemplateErrorsAndArguments(t *testing.T) {
	client := templateClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound"}`)
	})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"get", "5.9.9"}, `getting WIF configuration template "5.9.9": not found`},
		{[]string{"list"}, "listing WIF configuration templates: not found"},
		{[]string{"get"}, "accepts 1 arg(s)"},
		{[]string{"list", "extra"}, `unknown command "extra"`},
	} {
		out, err := runTemplateCommand(t, client, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) || out != "" {
			t.Errorf("args=%v: out=%q error=%v, want %q", tc.args, out, err, tc.want)
		}
	}
}

func TestListTemplateEmpty(t *testing.T) {
	client := templateClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"kind":"WIFConfigTemplateList","apiVersion":"gcp.managed.openshift.io/v1"}`)
	})
	out, err := runTemplateCommand(t, client, "list")
	if err != nil || out != "No WIF configuration templates found.\n" {
		t.Errorf("out=%q error=%v", out, err)
	}
	for _, format := range []string{"json", "yaml"} {
		out, err := runTemplateCommand(t, client, "list", "-o", format)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "[]") || strings.Contains(out, "null") {
			t.Errorf("empty %s items = %s", format, out)
		}
	}
}

func TestTemplateGetWrapsHTTPError(t *testing.T) {
	client := templateClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden"}`)
	})
	_, err := runTemplateCommand(t, client, "get", "5.0.0")
	var httpErr *platformapi.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode() != http.StatusForbidden {
		t.Errorf("error = %v, want wrapped 403", err)
	}
}
