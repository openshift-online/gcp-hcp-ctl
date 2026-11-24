package nodepool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNodePoolStatus(t *testing.T) {
	t.Run("When there are no conditions it should return Pending", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Status: gcpv1.NodePoolStatus{},
		}
		if got := nodePoolStatus(np); got != "Pending" {
			t.Errorf("expected 'Pending', got %q", got)
		}
	})

	t.Run("When NodePoolAvailable and NodePoolHealthy are True it should return Ready", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Status: gcpv1.NodePoolStatus{
				Conditions: []metav1.Condition{
					{Type: "NodePoolAvailable", Status: metav1.ConditionTrue},
					{Type: "NodePoolHealthy", Status: metav1.ConditionTrue},
				},
			},
		}
		if got := nodePoolStatus(np); got != "Ready" {
			t.Errorf("expected 'Ready', got %q", got)
		}
	})

	t.Run("When NodePoolAvailable is False it should return Progressing", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Status: gcpv1.NodePoolStatus{
				Conditions: []metav1.Condition{
					{Type: "NodePoolAvailable", Status: metav1.ConditionFalse, Reason: "NodePoolNotAvailable"},
				},
			},
		}
		if got := nodePoolStatus(np); got != "Progressing" {
			t.Errorf("expected 'Progressing', got %q", got)
		}
	})

	t.Run("When NodePoolAvailable is True and NodePoolHealthy is False it should return Degraded", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Status: gcpv1.NodePoolStatus{
				Conditions: []metav1.Condition{
					{Type: "NodePoolAvailable", Status: metav1.ConditionTrue},
					{Type: "NodePoolHealthy", Status: metav1.ConditionFalse, Reason: "NodePoolNotHealthy"},
				},
			},
		}
		if got := nodePoolStatus(np); got != "Degraded" {
			t.Errorf("expected 'Degraded', got %q", got)
		}
	})

	t.Run("When NodePoolAvailable is True and NodePoolHealthy is Unknown it should return Progressing", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Status: gcpv1.NodePoolStatus{
				Conditions: []metav1.Condition{
					{Type: "NodePoolAvailable", Status: metav1.ConditionTrue},
					{Type: "NodePoolHealthy", Status: metav1.ConditionUnknown, Reason: "NodePoolHealthCheckPending"},
				},
			},
		}
		if got := nodePoolStatus(np); got != "Progressing" {
			t.Errorf("expected 'Progressing', got %q", got)
		}
	})

	t.Run("When deleted_time is set it should return Deleting", func(t *testing.T) {
		now := metav1.NewTime(time.Now())
		np := &gcpv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				DeletionTimestamp: &now,
			},
			Status: gcpv1.NodePoolStatus{
				Conditions: []metav1.Condition{
					{Type: "NodePoolAvailable", Status: metav1.ConditionTrue},
					{Type: "NodePoolHealthy", Status: metav1.ConditionTrue},
				},
			},
		}
		if got := nodePoolStatus(np); got != "Deleting" {
			t.Errorf("expected 'Deleting', got %q", got)
		}
	})

	t.Run("When conditions exist but no NodePoolAvailable it should return Progressing", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Status: gcpv1.NodePoolStatus{
				Conditions: []metav1.Condition{
					{Type: "SomeOtherCondition", Status: metav1.ConditionTrue},
				},
			},
		}
		if got := nodePoolStatus(np); got != "Progressing" {
			t.Errorf("expected 'Progressing', got %q", got)
		}
	})
}

func TestNodePoolStatusDetail(t *testing.T) {
	t.Run("When Ready it should return just Ready without parenthetical", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Status: gcpv1.NodePoolStatus{
				Conditions: []metav1.Condition{
					{Type: "NodePoolAvailable", Status: metav1.ConditionTrue},
					{Type: "NodePoolHealthy", Status: metav1.ConditionTrue},
				},
			},
		}
		if got := nodePoolStatusDetail(np); got != "Ready" {
			t.Errorf("expected 'Ready', got %q", got)
		}
	})

	t.Run("When Pending it should return just Pending without parenthetical", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Status: gcpv1.NodePoolStatus{},
		}
		if got := nodePoolStatusDetail(np); got != "Pending" {
			t.Errorf("expected 'Pending', got %q", got)
		}
	})

	t.Run("When Deleting it should return just Deleting without parenthetical", func(t *testing.T) {
		now := metav1.NewTime(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
		np := &gcpv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				DeletionTimestamp: &now,
			},
			Status: gcpv1.NodePoolStatus{},
		}
		if got := nodePoolStatusDetail(np); got != "Deleting" {
			t.Errorf("expected 'Deleting', got %q", got)
		}
	})

	t.Run("When Deleting with progress it should show the full message in get but not list", func(t *testing.T) {
		now := metav1.NewTime(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
		message := "2 of 5 resources still being deleted: nodepools clusters-123/workers; hostedclusters clusters-123/my-cluster (KubeAPIError: waiting for IAM)"
		np := &gcpv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
			Status: gcpv1.NodePoolStatus{Conditions: []metav1.Condition{
				{Type: "NodePoolAvailable", Status: metav1.ConditionTrue},
				{Type: "DeletionProgressing", Status: metav1.ConditionTrue, Reason: "DeletionBlocked", Message: message},
			}},
		}
		if got, want := nodePoolStatusDetail(np), "Deleting ("+message+")"; got != want {
			t.Errorf("nodePoolStatusDetail() = %q, want %q", got, want)
		}
		if got := nodePoolStatus(np); got != "Deleting" {
			t.Errorf("nodePoolStatus() = %q, want Deleting", got)
		}
	})

	t.Run("When Deleting with no progress message it should show the reason", func(t *testing.T) {
		now := metav1.NewTime(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
		np := &gcpv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
			Status: gcpv1.NodePoolStatus{Conditions: []metav1.Condition{
				{Type: "DeletionProgressing", Status: metav1.ConditionTrue, Reason: "DeletionInProgress"},
			}},
		}
		if got := nodePoolStatusDetail(np); got != "Deleting (DeletionInProgress)" {
			t.Errorf("nodePoolStatusDetail() = %q, want Deleting (DeletionInProgress)", got)
		}
	})

	t.Run("When NodePoolAvailable is False it should return Progressing with the available condition message", func(t *testing.T) {
		np := &gcpv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status: gcpv1.NodePoolStatus{
				Conditions: []metav1.Condition{
					{Type: "NodePoolAvailable", Status: metav1.ConditionFalse, Reason: "NodePoolNotAvailable", Message: "Some nodes not available", ObservedGeneration: 1},
				},
			},
		}
		got := nodePoolStatusDetail(np)
		if got != "Progressing (Some nodes not available)" {
			t.Errorf("expected 'Progressing (Some nodes not available)', got %q", got)
		}
	})

	t.Run("When NodePoolAvailable is False with generation mismatch it should show generation detail", func(t *testing.T) {
		np := &gcpv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Generation: 3},
			Status: gcpv1.NodePoolStatus{
				Conditions: []metav1.Condition{
					{Type: "NodePoolAvailable", Status: metav1.ConditionFalse, Reason: "NodePoolNotAvailable", ObservedGeneration: 2},
				},
			},
		}
		got := nodePoolStatusDetail(np)
		if got != "Progressing (controller reconciling generation 3)" {
			t.Errorf("expected 'Progressing (controller reconciling generation 3)', got %q", got)
		}
	})

	t.Run("When NodePoolAvailable is False with long message it should truncate", func(t *testing.T) {
		np := &gcpv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status: gcpv1.NodePoolStatus{
				Conditions: []metav1.Condition{
					{Type: "NodePoolAvailable", Status: metav1.ConditionFalse, Reason: "NodePoolNotAvailable", Message: strings.Repeat("a", 80), ObservedGeneration: 1},
				},
			},
		}
		got := nodePoolStatusDetail(np)
		if !strings.Contains(got, "...") {
			t.Errorf("expected truncation ellipsis, got %q", got)
		}
		if !strings.HasPrefix(got, "Progressing (") {
			t.Errorf("expected 'Progressing (' prefix, got %q", got)
		}
	})

	t.Run("When NodePoolAvailable is False with no message it should fall back to reason", func(t *testing.T) {
		np := &gcpv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status: gcpv1.NodePoolStatus{
				Conditions: []metav1.Condition{
					{Type: "NodePoolAvailable", Status: metav1.ConditionFalse, Reason: "NodePoolNotAvailable", ObservedGeneration: 1},
				},
			},
		}
		got := nodePoolStatusDetail(np)
		if got != "Progressing (NodePoolNotAvailable)" {
			t.Errorf("expected 'Progressing (NodePoolNotAvailable)', got %q", got)
		}
	})

	t.Run("When Available is True but Healthy is False it should return Degraded with the health condition message", func(t *testing.T) {
		np := &gcpv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status: gcpv1.NodePoolStatus{
				Conditions: []metav1.Condition{
					{Type: "NodePoolAvailable", Status: metav1.ConditionTrue, Reason: "NodePoolAvailable"},
					{Type: "NodePoolHealthy", Status: metav1.ConditionFalse, Reason: "NodePoolNotHealthy", Message: "2 of 3 nodes not ready", ObservedGeneration: 1},
				},
			},
		}
		got := nodePoolStatusDetail(np)
		if got != "Degraded (2 of 3 nodes not ready)" {
			t.Errorf("expected 'Degraded (2 of 3 nodes not ready)', got %q", got)
		}
	})
}

func TestFindCondition(t *testing.T) {
	t.Run("When condition exists it should return it", func(t *testing.T) {
		conditions := []metav1.Condition{
			{Type: "Available", Status: metav1.ConditionTrue},
			{Type: "Reconciled", Status: metav1.ConditionFalse},
		}
		got := meta.FindStatusCondition(conditions, "Reconciled")
		if got == nil {
			t.Fatal("expected non-nil condition")
		}
		if got.Status != metav1.ConditionFalse {
			t.Errorf("expected status False, got %q", got.Status)
		}
	})

	t.Run("When condition does not exist it should return nil", func(t *testing.T) {
		conditions := []metav1.Condition{
			{Type: "Available", Status: metav1.ConditionTrue},
		}
		if got := meta.FindStatusCondition(conditions, "Reconciled"); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("When conditions slice is empty it should return nil", func(t *testing.T) {
		if got := meta.FindStatusCondition(nil, "Reconciled"); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

func TestReleaseVersion(t *testing.T) {
	t.Run("When release version is set it should return it", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{
				Release: gcpv1.NodePoolReleaseSpec{Version: "4.22.0"},
			},
		}
		if got := releaseVersion(np); got != "4.22.0" {
			t.Errorf("expected '4.22.0', got %q", got)
		}
	})

	t.Run("When version is empty it should return <none>", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{},
		}
		if got := releaseVersion(np); got != "<none>" {
			t.Errorf("expected '<none>', got %q", got)
		}
	})
}

func TestNodeCount(t *testing.T) {
	t.Run("When nodeCount is set it should return the count", func(t *testing.T) {
		r := int32(3)
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{NodeCount: &r},
		}
		if got := nodeCount(np); got != "3" {
			t.Errorf("expected '3', got %q", got)
		}
	})

	t.Run("When nodeCount is zero it should return 0", func(t *testing.T) {
		r := int32(0)
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{NodeCount: &r},
		}
		if got := nodeCount(np); got != "0" {
			t.Errorf("expected '0', got %q", got)
		}
	})

	t.Run("When nodeCount is nil it should return dash", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{},
		}
		if got := nodeCount(np); got != "-" {
			t.Errorf("expected '-', got %q", got)
		}
	})
}

func TestNpConditionSummary(t *testing.T) {
	t.Run("When observed generation is behind it should show generation detail", func(t *testing.T) {
		cond := &metav1.Condition{
			Status:             metav1.ConditionFalse,
			Reason:             "NotReconciled",
			ObservedGeneration: 2,
		}
		got := conditionSummary(cond, 3)
		if got != "controller reconciling generation 3" {
			t.Errorf("expected 'controller reconciling generation 3', got %q", got)
		}
	})

	t.Run("When observed generation is zero it should not show generation detail", func(t *testing.T) {
		cond := &metav1.Condition{
			Status:             metav1.ConditionFalse,
			Reason:             "NotReconciled",
			Message:            "waiting for adapters",
			ObservedGeneration: 0,
		}
		got := conditionSummary(cond, 3)
		if got != "waiting for adapters" {
			t.Errorf("expected 'waiting for adapters', got %q", got)
		}
	})

	t.Run("When message is present it should return the message", func(t *testing.T) {
		cond := &metav1.Condition{
			Status:             metav1.ConditionFalse,
			Reason:             "SomeReason",
			Message:            "detailed message",
			ObservedGeneration: 1,
		}
		got := conditionSummary(cond, 1)
		if got != "detailed message" {
			t.Errorf("expected 'detailed message', got %q", got)
		}
	})

	t.Run("When message exceeds 60 chars it should truncate", func(t *testing.T) {
		cond := &metav1.Condition{
			Status:             metav1.ConditionFalse,
			Reason:             "SomeReason",
			Message:            strings.Repeat("a", 80),
			ObservedGeneration: 1,
		}
		got := conditionSummary(cond, 1)
		if len(got) != 63 {
			t.Errorf("expected length 63 (60 + '...'), got %d", len(got))
		}
		if !strings.HasSuffix(got, "...") {
			t.Error("expected truncation suffix '...'")
		}
	})

	t.Run("When no message it should fall back to reason", func(t *testing.T) {
		cond := &metav1.Condition{
			Status:             metav1.ConditionFalse,
			Reason:             "AdaptersNotReady",
			ObservedGeneration: 1,
		}
		got := conditionSummary(cond, 1)
		if got != "AdaptersNotReady" {
			t.Errorf("expected 'AdaptersNotReady', got %q", got)
		}
	})

	t.Run("When both reason and message are empty it should return empty string", func(t *testing.T) {
		cond := &metav1.Condition{
			Status:             metav1.ConditionFalse,
			ObservedGeneration: 1,
		}
		got := conditionSummary(cond, 1)
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}

func TestMachineType(t *testing.T) {
	t.Run("When GCP platform is set it should return machine type", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{
				Platform: gcpv1.NodePoolPlatformSpec{
					GCP: &gcpv1.GCPNodePoolPlatform{MachineType: "n2-standard-4"},
				},
			},
		}
		if got := machineType(np); got != "n2-standard-4" {
			t.Errorf("expected 'n2-standard-4', got %q", got)
		}
	})

	t.Run("When GCP platform is nil it should return dash", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{
				Platform: gcpv1.NodePoolPlatformSpec{},
			},
		}
		if got := machineType(np); got != "-" {
			t.Errorf("expected '-', got %q", got)
		}
	})

	t.Run("When GCP platform is set but MachineType is empty it should return dash", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{
				Platform: gcpv1.NodePoolPlatformSpec{
					GCP: &gcpv1.GCPNodePoolPlatform{MachineType: ""},
				},
			},
		}
		if got := machineType(np); got != "-" {
			t.Errorf("expected '-', got %q", got)
		}
	})
}

func TestRegionFromZone(t *testing.T) {
	t.Run("When zone is set it should return the region prefix", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{
				Platform: gcpv1.NodePoolPlatformSpec{
					GCP: &gcpv1.GCPNodePoolPlatform{Zone: "northamerica-northeast2-a"},
				},
			},
		}
		if got := regionFromZone(np); got != "northamerica-northeast2" {
			t.Errorf("expected 'northamerica-northeast2', got %q", got)
		}
	})

	t.Run("When zone has a single-segment region it should return the region", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{
				Platform: gcpv1.NodePoolPlatformSpec{
					GCP: &gcpv1.GCPNodePoolPlatform{Zone: "us-central1-b"},
				},
			},
		}
		if got := regionFromZone(np); got != "us-central1" {
			t.Errorf("expected 'us-central1', got %q", got)
		}
	})

	t.Run("When GCP platform is nil it should return empty", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{
				Platform: gcpv1.NodePoolPlatformSpec{},
			},
		}
		if got := regionFromZone(np); got != "" {
			t.Errorf("expected '', got %q", got)
		}
	})

	t.Run("When zone is empty it should return empty", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{
				Platform: gcpv1.NodePoolPlatformSpec{
					GCP: &gcpv1.GCPNodePoolPlatform{Zone: ""},
				},
			},
		}
		if got := regionFromZone(np); got != "" {
			t.Errorf("expected '', got %q", got)
		}
	})

	t.Run("When zone is malformed without a hyphen it should return empty", func(t *testing.T) {
		np := &gcpv1.NodePool{
			Spec: gcpv1.NodePoolSpec{
				Platform: gcpv1.NodePoolPlatformSpec{
					GCP: &gcpv1.GCPNodePoolPlatform{Zone: "uscentral1"},
				},
			},
		}
		if got := regionFromZone(np); got != "" {
			t.Errorf("expected '', got %q", got)
		}
	})

	for _, zone := range []string{"us-central1-", "us-central1", "us-central1-9"} {
		t.Run("When zone is malformed: "+zone, func(t *testing.T) {
			np := &gcpv1.NodePool{
				Spec: gcpv1.NodePoolSpec{
					Platform: gcpv1.NodePoolPlatformSpec{
						GCP: &gcpv1.GCPNodePoolPlatform{Zone: zone},
					},
				},
			}
			if got := regionFromZone(np); got != "" {
				t.Errorf("expected empty region for %q, got %q", zone, got)
			}
		})
	}
}

func executeNodepoolServer(t *testing.T, handler http.HandlerFunc, args ...string) (error, string, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := platformapi.NewClientForTest(server.URL, "my-project")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	root := &cobra.Command{Use: "gcphcpctl", SilenceUsage: true}
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	group := NewNodePoolCmd(&config.Config{})
	group.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		cmd.SetContext(context.WithValue(cmd.Context(), clientKey, client))
		return nil
	}
	root.AddCommand(group)
	root.SetArgs(append([]string{"nodepool"}, args...))
	err = root.Execute()
	return err, stdout.String(), stderr.String()
}

func executeNodepoolError(t *testing.T, code int, body string, args ...string) (error, string, string) {
	t.Helper()
	return executeNodepoolServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}), args...)
}

func TestNodepoolCommandErrorBoundary(t *testing.T) {
	const secrets = `Bearer synthetic-token Authorization: Bearer synthetic-token alice@example.invalid https://user:password@example.invalid/?token=synthetic-token`
	for _, tc := range []struct {
		name       string
		code       int
		body, want string
		args       []string
		uncertain  bool
	}{
		{"list unauthorized", 401, `{"error":"forbidden","message":"` + secrets + `"}`, `listing nodepools: not authenticated`, []string{"list"}, false},
		{"get forbidden", 403, `{"error":"forbidden","message":"` + secrets + `"}`, `looking up nodepool "missing" in project "my-project": permission denied`, []string{"get", "missing"}, false},
		{"get missing", 404, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","message":"` + secrets + `"}`, `looking up nodepool "missing" in project "my-project": not found`, []string{"get", "missing"}, false},
		{"list forbidden", 403, `{"error":"forbidden","message":"` + secrets + `"}`, `listing nodepools: permission denied`, []string{"list"}, false},
		{"scale forbidden", 403, `{"error":"forbidden","message":"` + secrets + `"}`, `scaling nodepool test-pool: permission denied`, []string{"scale", "test-pool", "--replicas", "3"}, false},
		{"delete forbidden", 403, `{"error":"forbidden","message":"` + secrets + `"}`, `deleting nodepool test-pool: permission denied`, []string{"delete", "test-pool", "--confirm"}, false},
		{"create uncertain", 503, `{"error":"` + secrets + `"}`, `creating nodepool: service unavailable; check the resource before retrying`, []string{"create", "test-pool", "--cluster", "test-cluster", "--version", "4.22.0", "--zone", "us-central1-a", "--subnet", "test-subnet"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err, out, stderr := executeNodepoolError(t, tc.code, tc.body, tc.args...)
			t.Logf("stderr: %q", stderr)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if out != "" || stderr != "Error: "+tc.want+"\n" {
				t.Errorf("stdout = %q, stderr = %q", out, stderr)
			}
			if platformapi.IsUncertainOutcome(err) != tc.uncertain {
				t.Errorf("uncertain = %v", platformapi.IsUncertainOutcome(err))
			}
			var h *platformapi.HTTPError
			if !errors.As(err, &h) || h.StatusCode() != tc.code {
				t.Errorf("missing HTTP metadata: %v", err)
			}
			for _, s := range []string{"synthetic-token", "Authorization:", "alice@example.invalid", "user:password", "Platform API", "GET nodepools", "HTTP 40"} {
				if strings.Contains(fmt.Sprintf("%+v %v %s", err, err, stderr), s) {
					t.Errorf("leaked %q", s)
				}
			}
		})
	}
}

func TestNodepoolCreateInvalidSuccessDoesNotLeak(t *testing.T) {
	body := `{"kind":"NodePool","metadata":{"name":"Bearer synthetic-token"}`
	err, out, stderr := executeNodepoolError(t, 200, body, "create", "test-pool", "--cluster", "test-cluster", "--version", "4.22.0", "--zone", "us-central1-a", "--subnet", "test-subnet")
	want := "creating nodepool: server reported success, but its response was invalid; check the resource before retrying"
	var httpErr *platformapi.HTTPError
	if err == nil || err.Error() != want || !platformapi.IsUncertainOutcome(err) || errors.As(err, &httpErr) {
		t.Fatalf("invalid success error = %v", err)
	}
	if out != "" || stderr != "Error: "+want+"\n" || strings.Contains(fmt.Sprintf("%+v %s", err, stderr), "synthetic-token") {
		t.Fatalf("output = %q / %q", out, stderr)
	}
}

func TestNodepoolListShowsRegionColumn(t *testing.T) {
	// A creation timestamp six days in the past keeps the rendered AGE stable
	// ("6d"); RFC3339 formatting truncates sub-second precision downwards, so
	// the elapsed duration can only grow past the six-day boundary.
	created := time.Now().UTC().Add(-6 * 24 * time.Hour).Format(time.RFC3339)
	condition := func(condType string) string {
		return `{"type":"` + condType + `","status":"True","reason":"` + condType + `","lastTransitionTime":"` + created + `"}`
	}
	body := `{"apiVersion":"gcp.managed.openshift.io/v1","kind":"NodePoolList","items":[` +
		`{"apiVersion":"gcp.managed.openshift.io/v1","kind":"NodePool",` +
		`"metadata":{"name":"workers","creationTimestamp":"` + created + `"},` +
		`"spec":{"clusterID":"wif3-first","nodeCount":1,"release":{"version":"5.0.2"},` +
		`"platform":{"gcp":{"machineType":"n2-standard-4","zone":"us-central1-a"}}},` +
		`"status":{"conditions":[` + condition("NodePoolAvailable") + `,` + condition("NodePoolHealthy") + `]}}]}`

	err, out, stderr := executeNodepoolError(t, http.StatusOK, body, "list")
	if err != nil {
		t.Fatalf("listing nodepools: %v (stderr: %q)", err, stderr)
	}

	want := "NAME     CLUSTER     REGION       REPLICAS  INSTANCE       VERSION  STATUS  AGE\n" +
		"workers  wif3-first  us-central1  1         n2-standard-4  5.0.2    Ready   6d\n"
	if out != want {
		t.Errorf("list output =\n%q\nwant\n%q", out, want)
	}
}

func TestPrintNodePoolVersions(t *testing.T) {
	t.Run("renders target and current versions", func(t *testing.T) {
		np := &gcpv1.NodePool{}
		np.Name = "workers"
		np.Spec.Release.Version = "5.0.1"
		np.Status.Version = "5.0.0"

		var buf bytes.Buffer
		if err := printNodePool(&buf, np, "", "text"); err != nil {
			t.Fatalf("printNodePool: %v", err)
		}
		got := buf.String()
		for _, want := range []string{
			"Target Version:  5.0.1",
			"Current Version: 5.0.0",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("output missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("omits version lines when both are unset", func(t *testing.T) {
		np := &gcpv1.NodePool{}
		np.Name = "workers"

		var buf bytes.Buffer
		if err := printNodePool(&buf, np, "", "text"); err != nil {
			t.Fatalf("printNodePool: %v", err)
		}
		if strings.Contains(buf.String(), "Version:") {
			t.Errorf("unexpected version lines in output:\n%s", buf.String())
		}
	})

	t.Run("omits current version when status version is empty", func(t *testing.T) {
		np := &gcpv1.NodePool{}
		np.Name = "workers"
		np.Spec.Release.Version = "5.0.1"

		var buf bytes.Buffer
		if err := printNodePool(&buf, np, "", "text"); err != nil {
			t.Fatalf("printNodePool: %v", err)
		}
		got := buf.String()
		if !strings.Contains(got, "Target Version:  5.0.1\n") {
			t.Errorf("missing target version line:\n%s", got)
		}
		if strings.Contains(got, "Current Version:") {
			t.Errorf("unexpected current version line:\n%s", got)
		}
	})
}

func TestPrintNodePoolCreatedDeleted(t *testing.T) {
	np := &gcpv1.NodePool{}
	np.Name = "workers"
	np.CreationTimestamp = metav1.Time{Time: time.Now().UTC().Add(-6 * 24 * time.Hour)}
	np.DeletionTimestamp = &metav1.Time{Time: time.Now().UTC().Add(-2 * time.Hour)}

	var buf bytes.Buffer
	if err := printNodePool(&buf, np, "", "text"); err != nil {
		t.Fatalf("printNodePool: %v", err)
	}
	got := buf.String()

	created := np.CreationTimestamp.UTC().Format(time.RFC3339)
	if want := "Created:         " + created + " (6d)\n"; !strings.Contains(got, want) {
		t.Errorf("output missing %q:\n%s", want, got)
	}
	deleted := np.DeletionTimestamp.UTC().Format(time.RFC3339)
	if want := "Deleted:         " + deleted + " (2h)\n"; !strings.Contains(got, want) {
		t.Errorf("output missing %q:\n%s", want, got)
	}
	for _, stale := range []string{"CreatedAt", "DeletedAt"} {
		if strings.Contains(got, stale) {
			t.Errorf("output contains stale label %q:\n%s", stale, got)
		}
	}
}

func TestPrintNodePoolInfraID(t *testing.T) {
	newNodePool := func() *gcpv1.NodePool {
		np := &gcpv1.NodePool{}
		np.Name = "workers"
		np.Spec.ClusterID = "wif3-first"
		return np
	}

	t.Run("renders Infra ID after Cluster", func(t *testing.T) {
		var buf bytes.Buffer
		if err := printNodePool(&buf, newNodePool(), "wif3-first-abcde", "text"); err != nil {
			t.Fatalf("printNodePool: %v", err)
		}
		want := "Cluster:         wif3-first\nInfra ID:        wif3-first-abcde\n"
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q:\n%s", want, buf.String())
		}
	})

	t.Run("omits Infra ID when parent cluster is unknown", func(t *testing.T) {
		var buf bytes.Buffer
		if err := printNodePool(&buf, newNodePool(), "", "text"); err != nil {
			t.Fatalf("printNodePool: %v", err)
		}
		if strings.Contains(buf.String(), "Infra ID:") {
			t.Errorf("unexpected Infra ID line:\n%s", buf.String())
		}
	})
}

// nodepoolGetHandler serves a workers nodepool in project my-project plus a
// configurable response for its parent cluster lookup.
func nodepoolGetHandler(t *testing.T, created string, clusterCode int, clusterBody string) http.HandlerFunc {
	t.Helper()
	condition := func(condType string) string {
		return `{"type":"` + condType + `","status":"True","reason":"` + condType + `","lastTransitionTime":"` + created + `"}`
	}
	nodepoolBody := `{"apiVersion":"gcp.managed.openshift.io/v1","kind":"NodePool",` +
		`"metadata":{"name":"workers","uid":"11111111-2222-3333-4444-555555555555","creationTimestamp":"` + created + `","generation":1},` +
		`"spec":{"clusterID":"wif3-first","nodeCount":3,"release":{"version":"5.0.2"},` +
		`"platform":{"gcp":{"machineType":"n2-standard-4","diskSizeGB":100,"diskType":"pd-balanced","zone":"northamerica-northeast2-a"}}},` +
		`"status":{"version":"5.0.2","conditions":[` + condition("NodePoolAvailable") + `,` + condition("NodePoolHealthy") + `]}}`
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/apis/gcp.managed.openshift.io/v1/namespaces/my-project/nodepools/workers":
			_, _ = w.Write([]byte(nodepoolBody))
		case "/apis/gcp.managed.openshift.io/v1/namespaces/my-project/clusters/wif3-first":
			w.WriteHeader(clusterCode)
			_, _ = w.Write([]byte(clusterBody))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}
}

func TestNodepoolGetShowsInfraIDAndAge(t *testing.T) {
	created := time.Now().UTC().Add(-6 * 24 * time.Hour).Format(time.RFC3339)
	clusterBody := `{"apiVersion":"gcp.managed.openshift.io/v1","kind":"Cluster",` +
		`"metadata":{"name":"wif3-first"},"spec":{"infraID":"wif3-first-abcde"}}`

	err, out, stderr := executeNodepoolServer(t, nodepoolGetHandler(t, created, http.StatusOK, clusterBody), "get", "workers")
	if err != nil {
		t.Fatalf("getting nodepool: %v (stderr: %q)", err, stderr)
	}

	for _, want := range []string{
		"Cluster:         wif3-first\nInfra ID:        wif3-first-abcde\n",
		"Created:         " + created + " (6d)\n",
		"Status:          Ready\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "CreatedAt") {
		t.Errorf("output contains stale CreatedAt label:\n%s", out)
	}
}

func TestNodepoolGetOmitsInfraIDWhenClusterGone(t *testing.T) {
	created := time.Now().UTC().Add(-6 * 24 * time.Hour).Format(time.RFC3339)
	clusterBody := `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","message":"cluster not found"}`

	err, out, stderr := executeNodepoolServer(t, nodepoolGetHandler(t, created, http.StatusNotFound, clusterBody), "get", "workers")
	if err != nil {
		t.Fatalf("getting nodepool with missing parent cluster: %v (stderr: %q)", err, stderr)
	}
	if strings.Contains(out, "Infra ID:") {
		t.Errorf("unexpected Infra ID line for missing parent cluster:\n%s", out)
	}
	if !strings.Contains(out, "Cluster:         wif3-first\n") {
		t.Errorf("missing Cluster line:\n%s", out)
	}
	if stderr != "" {
		t.Errorf("unexpected warning for missing parent cluster: %q", stderr)
	}
}

func TestNodepoolGetPrintsWhenParentClusterLookupFails(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			created := time.Now().UTC().Add(-6 * 24 * time.Hour).Format(time.RFC3339)
			err, out, stderr := executeNodepoolServer(t, nodepoolGetHandler(t, created, code, `{}`), "get", "workers")
			if err != nil {
				t.Fatalf("getting nodepool: %v (stderr: %q)", err, stderr)
			}
			if !strings.Contains(out, "Cluster:         wif3-first\n") || !strings.Contains(out, "Name:            workers\n") {
				t.Errorf("nodepool details missing:\n%s", out)
			}
			if strings.Contains(out, "Infra ID:") {
				t.Errorf("unexpected Infra ID line:\n%s", out)
			}
			if !strings.Contains(stderr, `Warning: unable to look up parent cluster "wif3-first":`) {
				t.Errorf("missing parent lookup warning: %q", stderr)
			}
		})
	}
}

func TestNodepoolCreateParentInfraIDLookup(t *testing.T) {
	for _, tc := range []struct {
		name        string
		clusterCode int
		format      string
		wantInfraID bool
		wantWarning bool
	}{
		{"success", http.StatusOK, "text", true, false},
		{"forbidden", http.StatusForbidden, "text", false, true},
		{"server error", http.StatusInternalServerError, "text", false, true},
		{"JSON skips lookup", http.StatusForbidden, "json", false, false},
		{"YAML skips lookup", http.StatusForbidden, "yaml", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/apis/gcp.managed.openshift.io/v1/namespaces/my-project/nodepools":
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"apiVersion":"gcp.managed.openshift.io/v1","kind":"NodePool",` +
						`"metadata":{"name":"workers"},"spec":{"clusterID":"wif3-first",` +
						`"release":{"version":"5.0.2"},"platform":{"gcp":{"zone":"us-central1-a"}}}}`))
				case r.Method == http.MethodGet && r.URL.Path == "/apis/gcp.managed.openshift.io/v1/namespaces/my-project/clusters/wif3-first":
					w.WriteHeader(tc.clusterCode)
					_, _ = w.Write([]byte(`{"apiVersion":"gcp.managed.openshift.io/v1","kind":"Cluster",` +
						`"metadata":{"name":"wif3-first"},"spec":{"infraID":"wif3-first-abcde"}}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			})
			err, out, stderr := executeNodepoolServer(t, handler, "create", "workers", "--cluster", "wif3-first", "--version", "5.0.2", "--zone", "us-central1-a", "--subnet", "my-subnet", "-o", tc.format)
			if err != nil {
				t.Fatalf("creating nodepool: %v (stderr: %q)", err, stderr)
			}
			if !strings.Contains(out, "workers") {
				t.Errorf("created nodepool missing from output: %q", out)
			}
			if got := strings.Contains(out, "Infra ID:"); got != tc.wantInfraID {
				t.Errorf("Infra ID present = %v, want %v:\n%s", got, tc.wantInfraID, out)
			}
			if tc.wantInfraID && !strings.Contains(out, "Infra ID:        wif3-first-abcde\n") {
				t.Errorf("created nodepool missing parent Infra ID:\n%s", out)
			}
			if got := strings.Contains(stderr, `Warning: unable to look up parent cluster "wif3-first":`); got != tc.wantWarning {
				t.Errorf("warning present = %v, want %v: %q", got, tc.wantWarning, stderr)
			}
		})
	}
}
