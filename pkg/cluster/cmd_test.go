package cluster

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
)

func TestPtrStr(t *testing.T) {
	t.Run("When given a non-nil pointer it should return the string value", func(t *testing.T) {
		s := "hello"
		if got := ptrStr(&s); got != "hello" {
			t.Errorf("expected 'hello', got %q", got)
		}
	})

	t.Run("When given nil it should return empty string", func(t *testing.T) {
		if got := ptrStr(nil); got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}

func TestStrPtr(t *testing.T) {
	t.Run("When given a non-empty string it should return a pointer", func(t *testing.T) {
		p := strPtr("hello")
		if p == nil {
			t.Fatal("expected non-nil pointer")
		}
		if *p != "hello" {
			t.Errorf("expected 'hello', got %q", *p)
		}
	})

	t.Run("When given an empty string it should return nil", func(t *testing.T) {
		if got := strPtr(""); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

func TestTruncateID(t *testing.T) {
	t.Run("When ID is shorter than 12 chars it should return unchanged", func(t *testing.T) {
		if got := truncateID("abc"); got != "abc" {
			t.Errorf("expected 'abc', got %q", got)
		}
	})

	t.Run("When ID is exactly 12 chars it should return unchanged", func(t *testing.T) {
		id := "123456789012"
		if got := truncateID(id); got != id {
			t.Errorf("expected %q, got %q", id, got)
		}
	})

	t.Run("When ID is longer than 12 chars it should truncate with ellipsis", func(t *testing.T) {
		got := truncateID("1234567890123456")
		if got != "123456789012..." {
			t.Errorf("expected '123456789012...', got %q", got)
		}
	})

	t.Run("When ID is empty it should return empty", func(t *testing.T) {
		if got := truncateID(""); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
}

func TestClusterStatus(t *testing.T) {
	t.Run("When there are no conditions it should return Pending", func(t *testing.T) {
		c := &hyperfleet.Cluster{
			Status: hyperfleet.ClusterStatus{},
		}
		if got := clusterStatus(c); got != "Pending" {
			t.Errorf("expected 'Pending', got %q", got)
		}
	})

	t.Run("When Reconciled condition is True it should return Ready", func(t *testing.T) {
		c := &hyperfleet.Cluster{
			Status: hyperfleet.ClusterStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		if got := clusterStatus(c); got != "Ready" {
			t.Errorf("expected 'Ready', got %q", got)
		}
	})

	t.Run("When Reconciled is False without LastKnownReconciled it should return Progressing", func(t *testing.T) {
		reason := "AdaptersNotReady"
		c := &hyperfleet.Cluster{
			Status: hyperfleet.ClusterStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &reason},
				},
			},
		}
		if got := clusterStatus(c); got != "Progressing" {
			t.Errorf("expected 'Progressing', got %q", got)
		}
	})

	t.Run("When Reconciled is False and LastKnownReconciled is True it should return Degraded", func(t *testing.T) {
		reason := "AdaptersNotReady"
		c := &hyperfleet.Cluster{
			Status: hyperfleet.ClusterStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &reason},
					{Type: "LastKnownReconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		if got := clusterStatus(c); got != "Degraded" {
			t.Errorf("expected 'Degraded', got %q", got)
		}
	})

	t.Run("When an adapter condition is False it should return Progressing", func(t *testing.T) {
		reconciledReason := "NotReconciled"
		adapterReason := "HostedClusterFailed"
		c := &hyperfleet.Cluster{
			Status: hyperfleet.ClusterStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &reconciledReason},
					{Type: "HcAdapterSuccessful", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &adapterReason},
				},
			},
		}
		if got := clusterStatus(c); got != "Progressing" {
			t.Errorf("expected 'Progressing', got %q", got)
		}
	})

	t.Run("When deleted_time is set it should return Deleting", func(t *testing.T) {
		now := time.Now()
		c := &hyperfleet.Cluster{
			DeletedTime: &now,
			Status: hyperfleet.ClusterStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		if got := clusterStatus(c); got != "Deleting" {
			t.Errorf("expected 'Deleting', got %q", got)
		}
	})

	t.Run("When conditions exist but no Reconciled it should return Progressing", func(t *testing.T) {
		c := &hyperfleet.Cluster{
			Status: hyperfleet.ClusterStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Available", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		if got := clusterStatus(c); got != "Progressing" {
			t.Errorf("expected 'Progressing', got %q", got)
		}
	})
}

func TestClusterStatusDetail(t *testing.T) {
	t.Run("When Ready it should return just Ready without parenthetical", func(t *testing.T) {
		c := &hyperfleet.Cluster{
			Status: hyperfleet.ClusterStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		if got := clusterStatusDetail(c); got != "Ready" {
			t.Errorf("expected 'Ready', got %q", got)
		}
	})

	t.Run("When Pending it should return just Pending without parenthetical", func(t *testing.T) {
		c := &hyperfleet.Cluster{
			Status: hyperfleet.ClusterStatus{},
		}
		if got := clusterStatusDetail(c); got != "Pending" {
			t.Errorf("expected 'Pending', got %q", got)
		}
	})

	t.Run("When Deleting it should return just Deleting without parenthetical", func(t *testing.T) {
		now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
		c := &hyperfleet.Cluster{
			DeletedTime: &now,
			Status:      hyperfleet.ClusterStatus{},
		}
		got := clusterStatusDetail(c)
		if got != "Deleting" {
			t.Errorf("expected 'Deleting', got %q", got)
		}
	})

	t.Run("When Progressing with generation mismatch it should return Progressing without detail", func(t *testing.T) {
		reason := "NotReconciled"
		c := &hyperfleet.Cluster{
			Generation: 3,
			Status: hyperfleet.ClusterStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &reason, ObservedGeneration: 2},
				},
			},
		}
		got := clusterStatusDetail(c)
		if got != "Progressing" {
			t.Errorf("expected 'Progressing', got %q", got)
		}
	})

	t.Run("When adapter is False it should return Progressing not Error", func(t *testing.T) {
		reconciledReason := "NotReconciled"
		adapterReason := "HostedClusterFailed"
		c := &hyperfleet.Cluster{
			Generation: 1,
			Status: hyperfleet.ClusterStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &reconciledReason, ObservedGeneration: 1},
					{Type: "HcAdapterSuccessful", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &adapterReason, ObservedGeneration: 1},
				},
			},
		}
		got := clusterStatusDetail(c)
		if got != "Progressing" {
			t.Errorf("expected 'Progressing', got %q", got)
		}
	})

	t.Run("When Reconciled is False with nil Reason it should return Progressing", func(t *testing.T) {
		c := &hyperfleet.Cluster{
			Generation: 1,
			Status: hyperfleet.ClusterStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, ObservedGeneration: 1},
				},
			},
		}
		got := clusterStatusDetail(c)
		if got != "Progressing" {
			t.Errorf("expected 'Progressing', got %q", got)
		}
	})

	t.Run("When Degraded it should include the reconciled condition message", func(t *testing.T) {
		reason := "AdaptersNotReady"
		msg := "Some adapters not ready"
		c := &hyperfleet.Cluster{
			Generation: 1,
			Status: hyperfleet.ClusterStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &reason, Message: &msg, ObservedGeneration: 1},
					{Type: "LastKnownReconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		got := clusterStatusDetail(c)
		if got != "Degraded (Some adapters not ready)" {
			t.Errorf("expected 'Degraded (Some adapters not ready)', got %q", got)
		}
	})
}

func TestReleaseVersion(t *testing.T) {
	t.Run("When release version is set it should return it", func(t *testing.T) {
		v := "4.22.0"
		c := &hyperfleet.Cluster{
			Spec: hyperfleet.ClusterSpec{
				Release: &hyperfleet.ReleaseSpec{Version: &v},
			},
		}
		if got := releaseVersion(c); got != "4.22.0" {
			t.Errorf("expected '4.22.0', got %q", got)
		}
	})

	t.Run("When release is nil it should return <none>", func(t *testing.T) {
		c := &hyperfleet.Cluster{
			Spec: hyperfleet.ClusterSpec{},
		}
		if got := releaseVersion(c); got != "<none>" {
			t.Errorf("expected '<none>', got %q", got)
		}
	})

	t.Run("When version is empty string it should return <none>", func(t *testing.T) {
		empty := ""
		c := &hyperfleet.Cluster{
			Spec: hyperfleet.ClusterSpec{
				Release: &hyperfleet.ReleaseSpec{Version: &empty},
			},
		}
		if got := releaseVersion(c); got != "<none>" {
			t.Errorf("expected '<none>', got %q", got)
		}
	})

	t.Run("When version is nil it should return <none>", func(t *testing.T) {
		c := &hyperfleet.Cluster{
			Spec: hyperfleet.ClusterSpec{
				Release: &hyperfleet.ReleaseSpec{},
			},
		}
		if got := releaseVersion(c); got != "<none>" {
			t.Errorf("expected '<none>', got %q", got)
		}
	})
}

func TestFormatError(t *testing.T) {
	t.Run("When body is short it should include full body", func(t *testing.T) {
		resp := &http.Response{StatusCode: 400}
		body := []byte(`{"error":"bad request"}`)
		got := formatError(resp, body)
		if got != `HTTP 400: {"error":"bad request"}` {
			t.Errorf("unexpected: %q", got)
		}
	})

	t.Run("When response is nil with body it should include body in message", func(t *testing.T) {
		body := []byte(`{"error":"something broke"}`)
		got := formatError(nil, body)
		if got != `HTTP response unavailable: {"error":"something broke"}` {
			t.Errorf("unexpected: %q", got)
		}
	})

	t.Run("When response is nil with empty body it should return unavailable message", func(t *testing.T) {
		got := formatError(nil, nil)
		if got != "HTTP response unavailable" {
			t.Errorf("expected 'HTTP response unavailable', got %q", got)
		}
	})

	t.Run("When body exceeds 500 chars it should truncate", func(t *testing.T) {
		resp := &http.Response{StatusCode: 500}
		body := []byte(strings.Repeat("x", 600))
		got := formatError(resp, body)
		if !strings.HasPrefix(got, "HTTP 500: ") {
			t.Errorf("expected 'HTTP 500: ' prefix, got %q", got[:20])
		}
		if !strings.HasSuffix(got, "...") {
			t.Error("expected truncation suffix '...'")
		}
		// 10 ("HTTP 500: ") + 500 + 3 ("...") = 513
		if len(got) != 513 {
			t.Errorf("expected length 513, got %d", len(got))
		}
	})
}
