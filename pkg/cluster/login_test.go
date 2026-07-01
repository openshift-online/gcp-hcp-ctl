package cluster

import (
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/hyperfleet"
)

func dataPtr(m map[string]interface{}) *map[string]interface{} {
	return &m
}

func TestExtractEndpointFromStatuses(t *testing.T) {
	t.Run("When hc-adapter has apiEndpoint it should return it", func(t *testing.T) {
		wantEndpoint := "https://api.test-cluster.example.com"

		endpoint, err := extractEndpointFromStatuses([]hyperfleet.AdapterStatus{
			{Adapter: "placement-adapter", Data: dataPtr(map[string]interface{}{})},
			{
				Adapter: hcAdapterName,
				Data: dataPtr(map[string]interface{}{
					"hostedCluster": map[string]any{
						"apiEndpoint": wantEndpoint,
					},
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if endpoint != wantEndpoint {
			t.Errorf("got %q, want %q", endpoint, wantEndpoint)
		}
	})

	t.Run("When hc-adapter has no apiEndpoint it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromStatuses([]hyperfleet.AdapterStatus{
			{Adapter: hcAdapterName, Data: dataPtr(map[string]interface{}{})},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("When no hc-adapter exists it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromStatuses([]hyperfleet.AdapterStatus{
			{Adapter: "other-adapter", Data: dataPtr(map[string]interface{}{})},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("When items list is empty it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromStatuses(nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("When apiEndpoint is not HTTPS it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromStatuses([]hyperfleet.AdapterStatus{
			{
				Adapter: hcAdapterName,
				Data: dataPtr(map[string]interface{}{
					"hostedCluster": map[string]any{
						"apiEndpoint": "http://insecure.example.com",
					},
				}),
			},
		})
		if err == nil {
			t.Fatal("expected error for non-HTTPS endpoint, got nil")
		}
	})

	t.Run("When hc-adapter data is nil it should return an error", func(t *testing.T) {
		_, err := extractEndpointFromStatuses([]hyperfleet.AdapterStatus{
			{Adapter: hcAdapterName, Data: nil},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
