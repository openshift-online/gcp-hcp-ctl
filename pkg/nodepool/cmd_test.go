package nodepool

import (
	"net/http"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
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

func TestIntPtr(t *testing.T) {
	t.Run("When given a value it should return a pointer to that value", func(t *testing.T) {
		p := intPtr(42)
		if p == nil {
			t.Fatal("expected non-nil pointer")
		}
		if *p != 42 {
			t.Errorf("expected 42, got %d", *p)
		}
	})

	t.Run("When given zero it should return a pointer to zero", func(t *testing.T) {
		p := intPtr(0)
		if p == nil {
			t.Fatal("expected non-nil pointer")
		}
		if *p != 0 {
			t.Errorf("expected 0, got %d", *p)
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

func TestNodePoolStatus(t *testing.T) {
	t.Run("When there are no conditions it should return Pending", func(t *testing.T) {
		np := &hyperfleet.NodePool{
			Status: hyperfleet.NodePoolStatus{},
		}
		if got := nodePoolStatus(np); got != "Pending" {
			t.Errorf("expected 'Pending', got %q", got)
		}
	})

	t.Run("When Reconciled condition is True it should return Ready", func(t *testing.T) {
		np := &hyperfleet.NodePool{
			Status: hyperfleet.NodePoolStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		if got := nodePoolStatus(np); got != "Ready" {
			t.Errorf("expected 'Ready', got %q", got)
		}
	})

	t.Run("When Reconciled is False without LastKnownReconciled it should return Progressing", func(t *testing.T) {
		reason := "AdaptersNotReady"
		np := &hyperfleet.NodePool{
			Status: hyperfleet.NodePoolStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &reason},
				},
			},
		}
		if got := nodePoolStatus(np); got != "Progressing" {
			t.Errorf("expected 'Progressing', got %q", got)
		}
	})

	t.Run("When Reconciled is False and LastKnownReconciled is True it should return Degraded", func(t *testing.T) {
		reason := "AdaptersNotReady"
		np := &hyperfleet.NodePool{
			Status: hyperfleet.NodePoolStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &reason},
					{Type: "LastKnownReconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		if got := nodePoolStatus(np); got != "Degraded" {
			t.Errorf("expected 'Degraded', got %q", got)
		}
	})

	t.Run("When deleted_time is set it should return Deleting", func(t *testing.T) {
		now := time.Now()
		np := &hyperfleet.NodePool{
			DeletedTime: &now,
			Status: hyperfleet.NodePoolStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		if got := nodePoolStatus(np); got != "Deleting" {
			t.Errorf("expected 'Deleting', got %q", got)
		}
	})

	t.Run("When conditions exist but no Reconciled it should return Progressing", func(t *testing.T) {
		np := &hyperfleet.NodePool{
			Status: hyperfleet.NodePoolStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Available", Status: hyperfleet.ResourceConditionStatusTrue},
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
		np := &hyperfleet.NodePool{
			Status: hyperfleet.NodePoolStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		if got := nodePoolStatusDetail(np); got != "Ready" {
			t.Errorf("expected 'Ready', got %q", got)
		}
	})

	t.Run("When Pending it should return just Pending without parenthetical", func(t *testing.T) {
		np := &hyperfleet.NodePool{
			Status: hyperfleet.NodePoolStatus{},
		}
		if got := nodePoolStatusDetail(np); got != "Pending" {
			t.Errorf("expected 'Pending', got %q", got)
		}
	})

	t.Run("When Deleting it should return just Deleting without parenthetical", func(t *testing.T) {
		now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
		np := &hyperfleet.NodePool{
			DeletedTime: &now,
			Status:      hyperfleet.NodePoolStatus{},
		}
		if got := nodePoolStatusDetail(np); got != "Deleting" {
			t.Errorf("expected 'Deleting', got %q", got)
		}
	})

	t.Run("When Degraded it should include the reconciled condition message", func(t *testing.T) {
		reason := "AdaptersNotReady"
		msg := "Some adapters not ready"
		np := &hyperfleet.NodePool{
			Generation: 1,
			Status: hyperfleet.NodePoolStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &reason, Message: &msg, ObservedGeneration: 1},
					{Type: "LastKnownReconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		got := nodePoolStatusDetail(np)
		if got != "Degraded (Some adapters not ready)" {
			t.Errorf("expected 'Degraded (Some adapters not ready)', got %q", got)
		}
	})

	t.Run("When Degraded with generation mismatch it should show generation detail", func(t *testing.T) {
		reason := "NotReconciled"
		np := &hyperfleet.NodePool{
			Generation: 3,
			Status: hyperfleet.NodePoolStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &reason, ObservedGeneration: 2},
					{Type: "LastKnownReconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		got := nodePoolStatusDetail(np)
		if got != "Degraded (adapters finalizing generation 3)" {
			t.Errorf("expected 'Degraded (adapters finalizing generation 3)', got %q", got)
		}
	})

	t.Run("When Degraded with long message it should truncate", func(t *testing.T) {
		reason := "AdaptersNotReady"
		msg := strings.Repeat("a", 80)
		np := &hyperfleet.NodePool{
			Generation: 1,
			Status: hyperfleet.NodePoolStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &reason, Message: &msg, ObservedGeneration: 1},
					{Type: "LastKnownReconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		got := nodePoolStatusDetail(np)
		if !strings.Contains(got, "...") {
			t.Errorf("expected truncation ellipsis, got %q", got)
		}
		if !strings.HasPrefix(got, "Degraded (") {
			t.Errorf("expected 'Degraded (' prefix, got %q", got)
		}
	})

	t.Run("When Degraded with no message it should fall back to reason", func(t *testing.T) {
		reason := "AdaptersNotReady"
		np := &hyperfleet.NodePool{
			Generation: 1,
			Status: hyperfleet.NodePoolStatus{
				Conditions: []hyperfleet.ResourceCondition{
					{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse, Reason: &reason, ObservedGeneration: 1},
					{Type: "LastKnownReconciled", Status: hyperfleet.ResourceConditionStatusTrue},
				},
			},
		}
		got := nodePoolStatusDetail(np)
		if got != "Degraded (AdaptersNotReady)" {
			t.Errorf("expected 'Degraded (AdaptersNotReady)', got %q", got)
		}
	})
}

func TestFindCondition(t *testing.T) {
	t.Run("When condition exists it should return it", func(t *testing.T) {
		conditions := []hyperfleet.ResourceCondition{
			{Type: "Available", Status: hyperfleet.ResourceConditionStatusTrue},
			{Type: "Reconciled", Status: hyperfleet.ResourceConditionStatusFalse},
		}
		got := findCondition(conditions, "Reconciled")
		if got == nil {
			t.Fatal("expected non-nil condition")
		}
		if got.Status != hyperfleet.ResourceConditionStatusFalse {
			t.Errorf("expected status False, got %q", got.Status)
		}
	})

	t.Run("When condition does not exist it should return nil", func(t *testing.T) {
		conditions := []hyperfleet.ResourceCondition{
			{Type: "Available", Status: hyperfleet.ResourceConditionStatusTrue},
		}
		if got := findCondition(conditions, "Reconciled"); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("When conditions slice is empty it should return nil", func(t *testing.T) {
		if got := findCondition(nil, "Reconciled"); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

func TestReleaseVersion(t *testing.T) {
	t.Run("When release version is set it should return it", func(t *testing.T) {
		v := "4.22.0"
		np := &hyperfleet.NodePool{
			Spec: hyperfleet.NodePoolSpec{
				Release: &hyperfleet.NodePoolReleaseSpec{Version: &v},
			},
		}
		if got := releaseVersion(np); got != "4.22.0" {
			t.Errorf("expected '4.22.0', got %q", got)
		}
	})

	t.Run("When release is nil it should return <none>", func(t *testing.T) {
		np := &hyperfleet.NodePool{
			Spec: hyperfleet.NodePoolSpec{},
		}
		if got := releaseVersion(np); got != "<none>" {
			t.Errorf("expected '<none>', got %q", got)
		}
	})

	t.Run("When version is empty string it should return <none>", func(t *testing.T) {
		empty := ""
		np := &hyperfleet.NodePool{
			Spec: hyperfleet.NodePoolSpec{
				Release: &hyperfleet.NodePoolReleaseSpec{Version: &empty},
			},
		}
		if got := releaseVersion(np); got != "<none>" {
			t.Errorf("expected '<none>', got %q", got)
		}
	})

	t.Run("When version is nil it should return <none>", func(t *testing.T) {
		np := &hyperfleet.NodePool{
			Spec: hyperfleet.NodePoolSpec{
				Release: &hyperfleet.NodePoolReleaseSpec{},
			},
		}
		if got := releaseVersion(np); got != "<none>" {
			t.Errorf("expected '<none>', got %q", got)
		}
	})
}

func TestReplicas(t *testing.T) {
	t.Run("When replicas is set it should return the count", func(t *testing.T) {
		r := 3
		np := &hyperfleet.NodePool{
			Spec: hyperfleet.NodePoolSpec{Replicas: &r},
		}
		if got := replicas(np); got != "3" {
			t.Errorf("expected '3', got %q", got)
		}
	})

	t.Run("When replicas is zero it should return 0", func(t *testing.T) {
		r := 0
		np := &hyperfleet.NodePool{
			Spec: hyperfleet.NodePoolSpec{Replicas: &r},
		}
		if got := replicas(np); got != "0" {
			t.Errorf("expected '0', got %q", got)
		}
	})

	t.Run("When replicas is nil it should return dash", func(t *testing.T) {
		np := &hyperfleet.NodePool{
			Spec: hyperfleet.NodePoolSpec{},
		}
		if got := replicas(np); got != "-" {
			t.Errorf("expected '-', got %q", got)
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
		if len(got) != 513 {
			t.Errorf("expected length 513, got %d", len(got))
		}
	})
}

func TestNpConditionSummary(t *testing.T) {
	t.Run("When observed generation is behind it should show generation detail", func(t *testing.T) {
		reason := "NotReconciled"
		cond := &hyperfleet.ResourceCondition{
			Status:             hyperfleet.ResourceConditionStatusFalse,
			Reason:             &reason,
			ObservedGeneration: 2,
		}
		got := npConditionSummary(cond, 3)
		if got != "adapters finalizing generation 3" {
			t.Errorf("expected 'adapters finalizing generation 3', got %q", got)
		}
	})

	t.Run("When observed generation is zero it should not show generation detail", func(t *testing.T) {
		reason := "NotReconciled"
		msg := "waiting for adapters"
		cond := &hyperfleet.ResourceCondition{
			Status:             hyperfleet.ResourceConditionStatusFalse,
			Reason:             &reason,
			Message:            &msg,
			ObservedGeneration: 0,
		}
		got := npConditionSummary(cond, 3)
		if got != "waiting for adapters" {
			t.Errorf("expected 'waiting for adapters', got %q", got)
		}
	})

	t.Run("When message is present it should return the message", func(t *testing.T) {
		reason := "SomeReason"
		msg := "detailed message"
		cond := &hyperfleet.ResourceCondition{
			Status:             hyperfleet.ResourceConditionStatusFalse,
			Reason:             &reason,
			Message:            &msg,
			ObservedGeneration: 1,
		}
		got := npConditionSummary(cond, 1)
		if got != "detailed message" {
			t.Errorf("expected 'detailed message', got %q", got)
		}
	})

	t.Run("When message exceeds 60 chars it should truncate", func(t *testing.T) {
		reason := "SomeReason"
		msg := strings.Repeat("a", 80)
		cond := &hyperfleet.ResourceCondition{
			Status:             hyperfleet.ResourceConditionStatusFalse,
			Reason:             &reason,
			Message:            &msg,
			ObservedGeneration: 1,
		}
		got := npConditionSummary(cond, 1)
		if len(got) != 63 {
			t.Errorf("expected length 63 (60 + '...'), got %d", len(got))
		}
		if !strings.HasSuffix(got, "...") {
			t.Error("expected truncation suffix '...'")
		}
	})

	t.Run("When no message it should fall back to reason", func(t *testing.T) {
		reason := "AdaptersNotReady"
		cond := &hyperfleet.ResourceCondition{
			Status:             hyperfleet.ResourceConditionStatusFalse,
			Reason:             &reason,
			ObservedGeneration: 1,
		}
		got := npConditionSummary(cond, 1)
		if got != "AdaptersNotReady" {
			t.Errorf("expected 'AdaptersNotReady', got %q", got)
		}
	})

	t.Run("When both reason and message are nil it should return empty string", func(t *testing.T) {
		cond := &hyperfleet.ResourceCondition{
			Status:             hyperfleet.ResourceConditionStatusFalse,
			ObservedGeneration: 1,
		}
		got := npConditionSummary(cond, 1)
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}

func TestNodePoolFromCreateResponse(t *testing.T) {
	t.Run("When given a full response it should copy all fields", func(t *testing.T) {
		id := "np-123"
		href := "/api/v1/clusters/c1/nodepools/np-123"
		kind := "NodePool"
		labels := map[string]string{"shard": "1"}
		clusterID := "cluster-456"
		now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
		instanceType := "n2-standard-4"
		email := openapi_types.Email("test@example.com")

		cr := &hyperfleet.NodePoolCreateResponse{
			Id:          &id,
			Href:        &href,
			Kind:        &kind,
			Name:        "my-nodepool",
			Labels:      &labels,
			Generation:  1,
			CreatedBy:   email,
			CreatedTime: now,
			UpdatedBy:   email,
			UpdatedTime: now,
			OwnerReferences: hyperfleet.ObjectReference{
				Id: &clusterID,
			},
			Spec: hyperfleet.NodePoolSpec{
				Platform: hyperfleet.NodePoolPlatformSpec{
					Type: "gcp",
					Gcp: hyperfleet.GCPNodePoolPlatform{
						InstanceType: &instanceType,
					},
				},
			},
			Status: hyperfleet.NodePoolStatus{},
		}

		np := nodePoolFromCreateResponse(cr)

		if ptrStr(np.Id) != id {
			t.Errorf("Id: expected %q, got %q", id, ptrStr(np.Id))
		}
		if ptrStr(np.Href) != href {
			t.Errorf("Href: expected %q, got %q", href, ptrStr(np.Href))
		}
		if np.Name != "my-nodepool" {
			t.Errorf("Name: expected 'my-nodepool', got %q", np.Name)
		}
		if np.Generation != 1 {
			t.Errorf("Generation: expected 1, got %d", np.Generation)
		}
		if np.CreatedTime != now {
			t.Errorf("CreatedTime: expected %v, got %v", now, np.CreatedTime)
		}
		if np.UpdatedTime != now {
			t.Errorf("UpdatedTime: expected %v, got %v", now, np.UpdatedTime)
		}
		if np.DeletedTime != nil {
			t.Errorf("DeletedTime: expected nil, got %v", np.DeletedTime)
		}
		if np.DeletedBy != nil {
			t.Errorf("DeletedBy: expected nil, got %v", np.DeletedBy)
		}
		if ptrStr(np.OwnerReferences.Id) != clusterID {
			t.Errorf("OwnerReferences.Id: expected %q, got %q", clusterID, ptrStr(np.OwnerReferences.Id))
		}
	})

	t.Run("When response has deleted fields it should preserve them", func(t *testing.T) {
		now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
		deletedBy := openapi_types.Email("admin@example.com")

		cr := &hyperfleet.NodePoolCreateResponse{
			Name:        "deleted-np",
			Generation:  2,
			CreatedTime: now,
			DeletedTime: &now,
			DeletedBy:   &deletedBy,
			UpdatedTime: now,
			OwnerReferences: hyperfleet.ObjectReference{},
			Spec: hyperfleet.NodePoolSpec{
				Platform: hyperfleet.NodePoolPlatformSpec{
					Type: "gcp",
					Gcp:  hyperfleet.GCPNodePoolPlatform{},
				},
			},
			Status: hyperfleet.NodePoolStatus{},
		}

		np := nodePoolFromCreateResponse(cr)

		if np.DeletedTime == nil {
			t.Fatal("DeletedTime: expected non-nil")
		}
		if *np.DeletedTime != now {
			t.Errorf("DeletedTime: expected %v, got %v", now, *np.DeletedTime)
		}
		if np.DeletedBy == nil {
			t.Fatal("DeletedBy: expected non-nil")
		}
		if string(*np.DeletedBy) != "admin@example.com" {
			t.Errorf("DeletedBy: expected 'admin@example.com', got %q", *np.DeletedBy)
		}
	})
}
