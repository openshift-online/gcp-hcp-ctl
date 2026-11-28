package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/discovery"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/internal/access"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
)

type staticAuthorizationResolver struct {
	info       discovery.AuthorizationInfo
	err        error
	seenRegion *string
}

func (resolver staticAuthorizationResolver) Authorization(_ context.Context, cfg *config.Config) (discovery.AuthorizationInfo, error) {
	if resolver.seenRegion != nil {
		*resolver.seenRegion = cfg.Region
	}
	return resolver.info, resolver.err
}

func TestAccessCommandDiscovery(t *testing.T) {
	for group, verbs := range map[string][]string{"role": {"create", "update", "list", "get", "delete"}, "rolebinding": {"create", "update", "list", "get", "delete"}} {
		for _, verb := range verbs {
			cmd, remaining, err := newRootCmd().Find([]string{group, verb})
			if err != nil || len(remaining) != 0 || cmd.Name() != verb || cmd.Parent().Name() != group {
				t.Fatalf("%s %s: %v", group, verb, err)
			}
			if cmd.Flags().Lookup("output") != nil {
				t.Fatal("resource shadows inherited output")
			}
		}
	}
}

func TestAccessResolvedConfiguration(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		for _, override := range []bool{false, true} {
			t.Run(fmt.Sprint(format, override), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/apis/gcp.managed.openshift.io/v1/namespaces/customer/roles" {
						t.Error(r.URL)
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"apiVersion":"gcp.managed.openshift.io/v1","kind":"RoleList","items":[]}`)
				}))
				defer server.Close()
				configPath := filepath.Join(t.TempDir(), "config.yaml")
				if err := os.WriteFile(configPath, []byte("project: customer\napi_endpoint: "+server.URL+"\noutput: "+format+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				c, _ := platformapi.NewClientForTest(server.URL, "customer")
				root := newRootCmd()
				root.SetContext(access.WithClient(context.Background(), c))
				var out, stderr bytes.Buffer
				root.SetOut(&out)
				root.SetErr(&stderr)
				args := []string{"role", "list", "--config", configPath}
				if override {
					args = append(args, "--output", "text")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				project, _ := root.PersistentFlags().GetString("project")
				apiEndpoint, _ := root.PersistentFlags().GetString("api-endpoint")
				if project != "customer" || apiEndpoint != server.URL {
					t.Fatal(project, apiEndpoint)
				}
				if override {
					if !strings.Contains(out.String(), "PERMISSIONS") {
						t.Fatal(out.String())
					}
				} else if !strings.Contains(out.String(), "apiVersion") {
					t.Fatal(out.String())
				}
				if stderr.Len() != 0 {
					t.Fatal(stderr.String())
				}
			})
		}
	}
}

func TestAccessV2WithoutGlobalEndpointDoesNotSendRequest(t *testing.T) {
	t.Setenv("GCPHCPCTL_API_ENDPOINT", "")
	t.Setenv("GCPHCPCTL_REGION", "")
	t.Setenv("GCPHCPCTL_ENVIRONMENT", "")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("project: customer\nregion: us-east4\nenvironment: integration\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("Role or RoleBinding request was sent despite v2 omitting the global authorization endpoint")
	}))
	defer server.Close()
	client, err := platformapi.NewClientForTest(server.URL, "customer")
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{"role", "rolebinding"} {
		t.Run(group, func(t *testing.T) {
			unavailable := errors.New("global authorization endpoint is not configured")
			seenRegion := ""
			resolver := staticAuthorizationResolver{err: unavailable, seenRegion: &seenRegion}
			ctx := access.WithClient(access.WithResolver(context.Background(), resolver), client)
			root := newRootCmd()
			root.SetContext(ctx)
			root.SetArgs([]string{group, "list", "--config", configPath})
			if err := root.Execute(); !errors.Is(err, unavailable) {
				t.Fatalf("expected global authorization error, got %v", err)
			}
			if seenRegion != "us-east4" {
				t.Fatalf("expected inherited region, got %q", seenRegion)
			}
		})
	}
}

func TestAccessGlobalEndpointWithoutRegion(t *testing.T) {
	// An invalid URL makes client construction fail before authentication or a
	// network request, proving that the resolved endpoint reached NewClient.
	const stableURL = "http://authz.int.gcp-hcp.devshift.net"
	for _, group := range []string{"role", "rolebinding"} {
		t.Run(group, func(t *testing.T) {
			ctx := access.WithResolver(context.Background(), staticAuthorizationResolver{info: discovery.AuthorizationInfo{Endpoint: stableURL}})
			root := newRootCmd()
			root.SetContext(ctx)
			root.SetArgs([]string{group, "list", "--project", "customer", "--env", "int", "--config", filepath.Join(t.TempDir(), "missing.yaml")})
			if err := root.Execute(); err == nil || err.Error() != "API endpoint must use HTTPS" {
				t.Fatalf("resolved endpoint was not used without a region: %v", err)
			}
		})
	}
}
