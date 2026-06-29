package cluster

import (
	"encoding/json"
	"testing"
)

func TestExtractEndpointFromBody(t *testing.T) {
	t.Run("When hc-adapter has apiEndpoint it should return it", func(t *testing.T) {
		wantEndpoint := "https://api.test-cluster.example.com"

		endpoint, err := extractEndpointFromBody([]byte(mustJSON(t, adapterStatusList{
			Items: []adapterStatusItem{
				{Adapter: "placement-adapter", Data: map[string]any{}},
				{
					Adapter: "hc-adapter",
					Data: map[string]any{
						"hostedCluster": map[string]any{
							"apiEndpoint": wantEndpoint,
						},
					},
				},
			},
		})))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if endpoint != wantEndpoint {
			t.Errorf("got %q, want %q", endpoint, wantEndpoint)
		}
	})

	t.Run("When hc-adapter has no apiEndpoint it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromBody([]byte(mustJSON(t, adapterStatusList{
			Items: []adapterStatusItem{
				{Adapter: "hc-adapter", Data: map[string]any{}},
			},
		})))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("When no hc-adapter exists it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromBody([]byte(mustJSON(t, adapterStatusList{
			Items: []adapterStatusItem{
				{Adapter: "other-adapter", Data: map[string]any{}},
			},
		})))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("When items list is empty it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromBody([]byte(mustJSON(t, adapterStatusList{})))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("When apiEndpoint is not HTTPS it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromBody([]byte(mustJSON(t, adapterStatusList{
			Items: []adapterStatusItem{
				{
					Adapter: "hc-adapter",
					Data: map[string]any{
						"hostedCluster": map[string]any{
							"apiEndpoint": "http://insecure.example.com",
						},
					},
				},
			},
		})))
		if err == nil {
			t.Fatal("expected error for non-HTTPS endpoint, got nil")
		}
	})
}

func TestTruncate(t *testing.T) {
	t.Run("When string is shorter than max it should return it unchanged", func(t *testing.T) {
		if got := truncate("hello", 10); got != "hello" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("When string exceeds max it should truncate with ellipsis", func(t *testing.T) {
		got := truncate("hello world", 5)
		if got != "hello..." {
			t.Errorf("got %q, want hello...", got)
		}
	})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling JSON: %v", err)
	}
	return string(data)
}
