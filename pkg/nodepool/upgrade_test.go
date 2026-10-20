package nodepool

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUpgradeBuildRequest(t *testing.T) {
	options := &upgradeOptions{clusterRef: "my-cluster", version: "4.22.17", start: "2026-10-21T02:00:00-04:00"}
	request, err := options.buildRequest("workers", "my-project")
	if err != nil {
		t.Fatal(err)
	}
	if request.Kind != "NodePoolUpgradeRequest" || request.GenerateName != "nodepool-upgrade-" || request.Namespace != "my-project" {
		t.Errorf("metadata = %#v", request)
	}
	if request.Spec.ClusterID != "my-cluster" || request.Spec.NodePoolID != "workers" || request.Spec.TargetVersion != "4.22.17" {
		t.Errorf("spec = %#v", request.Spec)
	}
	if request.Spec.StartTime == nil || request.Spec.StartTime.UTC().Format(time.RFC3339) != "2026-10-21T06:00:00Z" {
		t.Errorf("startTime = %v", request.Spec.StartTime)
	}
	if request.Status != nil {
		t.Errorf("new request status = %#v", request.Status)
	}
}

func TestUpgradeBuildRequestRejectsInvalidInputs(t *testing.T) {
	for _, tc := range []struct {
		name, pool string
		options    upgradeOptions
	}{
		{"missing pool", "", upgradeOptions{clusterRef: "cluster", version: "4.22.17"}},
		{"missing cluster", "pool", upgradeOptions{version: "4.22.17"}},
		{"invalid version", "pool", upgradeOptions{clusterRef: "cluster", version: "4.22"}},
		{"invalid start", "pool", upgradeOptions{clusterRef: "cluster", version: "4.22.17", start: "tomorrow"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.options.buildRequest(tc.pool, "project"); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestUpgradeCommandAndStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			_, _ = w.Write([]byte(`{"apiVersion":"gcp.managed.openshift.io/v1","kind":"NodePoolUpgradeRequest","metadata":{"name":"nodepool-upgrade-abcde"},"spec":{"clusterID":"my-cluster","nodePoolID":"workers","targetVersion":"4.22.17"}}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"apiVersion":"gcp.managed.openshift.io/v1","kind":"NodePoolUpgradeRequest","metadata":{"name":"nodepool-upgrade-abcde"},"spec":{"clusterID":"my-cluster","nodePoolID":"workers","targetVersion":"4.22.17"},"status":{"conditions":[{"type":"Completed","status":"True","reason":"UpgradeCompleted","message":"NodePool reached the requested version","lastTransitionTime":"2026-10-20T20:00:00Z"}]}}`))
		default:
			t.Errorf("method = %s", r.Method)
		}
	}))
	defer server.Close()
	client, err := platformapi.NewClientForTest(server.URL, "my-project")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{"create", "NodePool upgrade request nodepool-upgrade-abcde created", []string{"upgrade", "workers", "--cluster", "my-cluster", "--version", "4.22.17"}},
		{"status", "Completed  True", []string{"upgrade", "status", "nodepool-upgrade-abcde", "--cluster", "my-cluster"}},
		{"structured", "kind: NodePoolUpgradeRequest", []string{"upgrade", "workers", "--cluster", "my-cluster", "--version", "4.22.17", "-o", "yaml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			root := &cobra.Command{Use: "gcphcpctl"}
			root.SetOut(&output)
			group := NewNodePoolCmd(&config.Config{})
			group.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
				cmd.SetContext(context.WithValue(cmd.Context(), clientKey, client))
				return nil
			}
			root.AddCommand(group)
			root.SetArgs(append([]string{"nodepool"}, tc.args...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), tc.want) {
				t.Errorf("output = %q, want substring %q", output.String(), tc.want)
			}
		})
	}
}

func TestPrintUpgradeStatusPending(t *testing.T) {
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	request := &platformapi.NodePoolUpgradeRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "nodepool-upgrade-abcde"},
		Spec:       platformapi.NodePoolUpgradeRequestSpec{ClusterID: "cluster", NodePoolID: "workers", TargetVersion: "4.22.17"},
	}
	if err := printUpgradeStatus(cmd, request); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Status:     Pending") {
		t.Errorf("output = %q", output.String())
	}
}

func TestPrintUpgradeStatusCompletedWithoutConditions(t *testing.T) {
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	completed := metav1.NewTime(time.Date(2026, 10, 20, 22, 14, 49, 0, time.UTC))
	request := &platformapi.NodePoolUpgradeRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "nodepool-upgrade-abcde"},
		Spec:       platformapi.NodePoolUpgradeRequestSpec{ClusterID: "cluster", NodePoolID: "workers", TargetVersion: "4.22.17"},
		Status:     &platformapi.NodePoolUpgradeRequestStatus{CompletedTime: &completed},
	}
	if err := printUpgradeStatus(cmd, request); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Status:     Completed") || !strings.Contains(output.String(), "Completed:  2026-10-20T22:14:49Z") {
		t.Errorf("output = %q", output.String())
	}
}
