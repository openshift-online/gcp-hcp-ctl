package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/network"
)

func TestAssemblePayload(t *testing.T) {
	t.Run("When given valid IAM output it should produce correct JSON", func(t *testing.T) {
		iamOutput := &iam.CreateOutput{
			ProjectID:     "my-project",
			ProjectNumber: "123456789",
			InfraID:       "test-cluster",
			WorkloadIdentityPool: iam.WorkloadIdentityConfig{
				PoolID:     "my-pool",
				ProviderID: "my-provider",
			},
			ServiceAccounts: map[string]string{
				"ctrlplane-op":     "ctrlplane@my-project.iam.gserviceaccount.com",
				"nodepool-mgmt":    "nodepool@my-project.iam.gserviceaccount.com",
				"cloud-controller": "cloud-ctrl@my-project.iam.gserviceaccount.com",
				"gcp-pd-csi":       "storage@my-project.iam.gserviceaccount.com",
				"image-registry":   "registry@my-project.iam.gserviceaccount.com",
				"cloud-network":    "network@my-project.iam.gserviceaccount.com",
			},
		}

		opts := buildPayloadOptions{
			endpointAccess: "PublicAndPrivate",
			oidcEndpoint:   "https://oidc.example.com",
			version:        "4.22.0",
			channelGroup:   "candidate",
		}

		netOutput := &network.CreateOutput{
			NetworkName: "my-vpc",
			SubnetName:  "my-subnet",
		}
		payload, err := assemblePayload("test-cluster", "test-cluster", "my-project", "us-central1", iamOutput, netOutput, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(payload, &result); err != nil {
			t.Fatalf("payload is not valid JSON: %v", err)
		}

		if result["name"] != "test-cluster" {
			t.Errorf("expected name 'test-cluster', got %v", result["name"])
		}
		if result["kind"] != "Cluster" {
			t.Errorf("expected kind 'Cluster', got %v", result["kind"])
		}

		spec, ok := result["spec"].(map[string]interface{})
		if !ok {
			t.Fatal("spec is not a map")
		}
		if spec["issuerURL"] != "https://oidc.example.com/test-cluster" {
			t.Errorf("expected issuerURL 'https://oidc.example.com/test-cluster', got %v", spec["issuerURL"])
		}
		if spec["infraID"] != "test-cluster" {
			t.Errorf("expected infraID 'test-cluster', got %v", spec["infraID"])
		}

		release, ok := spec["release"].(map[string]interface{})
		if !ok {
			t.Fatal("release is not a map")
		}
		if release["version"] != "4.22.0" {
			t.Errorf("expected version '4.22.0', got %v", release["version"])
		}
		if release["channelGroup"] != "candidate" {
			t.Errorf("expected channelGroup 'candidate', got %v", release["channelGroup"])
		}

		platform, ok := spec["platform"].(map[string]interface{})
		if !ok {
			t.Fatal("platform is not a map")
		}
		gcp, ok := platform["gcp"].(map[string]interface{})
		if !ok {
			t.Fatal("gcp is not a map")
		}
		if gcp["projectID"] != "my-project" {
			t.Errorf("expected projectID 'my-project', got %v", gcp["projectID"])
		}
		if gcp["region"] != "us-central1" {
			t.Errorf("expected region 'us-central1', got %v", gcp["region"])
		}
		if gcp["network"] != "my-vpc" {
			t.Errorf("expected network 'my-vpc', got %v", gcp["network"])
		}
		if gcp["subnet"] != "my-subnet" {
			t.Errorf("expected subnet 'my-subnet', got %v", gcp["subnet"])
		}
	})

	t.Run("When version is empty it should omit release", func(t *testing.T) {
		iamOutput := &iam.CreateOutput{
			ProjectID:     "proj",
			ProjectNumber: "999",
			InfraID:       "cl",
			WorkloadIdentityPool: iam.WorkloadIdentityConfig{
				PoolID:     "p",
				ProviderID: "pr",
			},
			ServiceAccounts: map[string]string{
				"ctrlplane-op": "a@p.iam", "nodepool-mgmt": "b@p.iam",
				"cloud-controller": "c@p.iam", "gcp-pd-csi": "d@p.iam",
				"image-registry": "e@p.iam", "cloud-network": "f@p.iam",
			},
		}
		netOutput := &network.CreateOutput{NetworkName: "net", SubnetName: "sub"}

		opts := buildPayloadOptions{
			endpointAccess: "Private",
			oidcEndpoint:   "https://oidc.test",
		}

		payload, err := assemblePayload("cl", "cl", "proj", "us-east1", iamOutput, netOutput, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(payload, &result); err != nil {
			t.Fatalf("payload is not valid JSON: %v", err)
		}

		spec := result["spec"].(map[string]interface{})
		if _, exists := spec["release"]; exists {
			t.Error("expected release to be omitted when version is empty")
		}
	})

	t.Run("When network config is missing networkName it should return error", func(t *testing.T) {
		iamOutput := &iam.CreateOutput{
			ProjectID: "proj", ProjectNumber: "999",
			WorkloadIdentityPool: iam.WorkloadIdentityConfig{PoolID: "p", ProviderID: "pr"},
			ServiceAccounts: map[string]string{
				"ctrlplane-op": "a@p.iam", "nodepool-mgmt": "b@p.iam",
				"cloud-controller": "c@p.iam", "gcp-pd-csi": "d@p.iam",
				"image-registry": "e@p.iam", "cloud-network": "f@p.iam",
			},
		}
		netOutput := &network.CreateOutput{SubnetName: "sub"}

		opts := buildPayloadOptions{endpointAccess: "Private", oidcEndpoint: "https://oidc.test"}

		_, err := assemblePayload("cl", "cl", "proj", "us-east1", iamOutput, netOutput, opts)
		if err == nil {
			t.Error("expected error for missing networkName")
		}
	})

	t.Run("When IAM config is missing required service account it should return error", func(t *testing.T) {
		iamOutput := &iam.CreateOutput{
			ProjectID: "proj", ProjectNumber: "999",
			WorkloadIdentityPool: iam.WorkloadIdentityConfig{PoolID: "p", ProviderID: "pr"},
			ServiceAccounts:      map[string]string{},
		}
		netOutput := &network.CreateOutput{NetworkName: "net", SubnetName: "sub"}

		opts := buildPayloadOptions{endpointAccess: "Private", oidcEndpoint: "https://oidc.test"}

		_, err := assemblePayload("cl", "cl", "proj", "us-east1", iamOutput, netOutput, opts)
		if err == nil {
			t.Error("expected error for missing service accounts")
		}
	})
}

func TestBuildPayloadFromConfigs(t *testing.T) {
	t.Run("When given valid config files it should assemble payload", func(t *testing.T) {
		dir := t.TempDir()

		iamConfig := iam.CreateOutput{
			ProjectID:     "test-proj",
			ProjectNumber: "111222",
			InfraID:       "my-infra",
			WorkloadIdentityPool: iam.WorkloadIdentityConfig{
				PoolID:     "pool-1",
				ProviderID: "prov-1",
			},
			ServiceAccounts: map[string]string{
				"ctrlplane-op":     "cp@test-proj.iam.gserviceaccount.com",
				"nodepool-mgmt":    "np@test-proj.iam.gserviceaccount.com",
				"cloud-controller": "cc@test-proj.iam.gserviceaccount.com",
				"gcp-pd-csi":       "pd@test-proj.iam.gserviceaccount.com",
				"image-registry":   "ir@test-proj.iam.gserviceaccount.com",
				"cloud-network":    "cn@test-proj.iam.gserviceaccount.com",
			},
		}
		iamFile := filepath.Join(dir, "iam.json")
		iamData, err := json.Marshal(iamConfig)
		if err != nil {
			t.Fatalf("marshaling IAM config: %v", err)
		}
		if err := os.WriteFile(iamFile, iamData, 0644); err != nil {
			t.Fatalf("writing IAM config: %v", err)
		}

		networkConfig := map[string]string{
			"region":      "europe-west1",
			"networkName": "net-1",
			"subnetName":  "sub-1",
		}
		netFile := filepath.Join(dir, "net.json")
		netData, err := json.Marshal(networkConfig)
		if err != nil {
			t.Fatalf("marshaling network config: %v", err)
		}
		if err := os.WriteFile(netFile, netData, 0644); err != nil {
			t.Fatalf("writing network config: %v", err)
		}

		opts := buildPayloadOptions{
			endpointAccess: "PublicAndPrivate",
			oidcEndpoint:   "https://oidc.test",
			version:        "4.21.0",
		}

		payload, err := buildPayloadFromConfigs("my-cluster", iamFile, netFile, "", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(payload, &result); err != nil {
			t.Fatalf("unmarshaling payload: %v", err)
		}

		if result["name"] != "my-cluster" {
			t.Errorf("expected name 'my-cluster', got %v", result["name"])
		}

		spec := result["spec"].(map[string]interface{})
		if spec["infraID"] != "my-infra" {
			t.Errorf("expected infraID from IAM config 'my-infra', got %v", spec["infraID"])
		}

		platform := spec["platform"].(map[string]interface{})
		gcp := platform["gcp"].(map[string]interface{})
		if gcp["region"] != "europe-west1" {
			t.Errorf("expected region from network config 'europe-west1', got %v", gcp["region"])
		}
		if gcp["network"] != "net-1" {
			t.Errorf("expected network 'net-1', got %v", gcp["network"])
		}
	})

	t.Run("When IAM config file does not exist it should return error", func(t *testing.T) {
		opts := buildPayloadOptions{oidcEndpoint: "https://oidc.test"}
		_, err := buildPayloadFromConfigs("cl", "/nonexistent/iam.json", "", "", opts)
		if err == nil {
			t.Error("expected error for missing IAM config file")
		}
	})

	t.Run("When IAM config has no infraID it should default to cluster name", func(t *testing.T) {
		dir := t.TempDir()

		iamConfig := iam.CreateOutput{
			ProjectID:     "proj",
			ProjectNumber: "999",
			WorkloadIdentityPool: iam.WorkloadIdentityConfig{
				PoolID:     "p",
				ProviderID: "pr",
			},
			ServiceAccounts: map[string]string{
				"ctrlplane-op": "a@p.iam", "nodepool-mgmt": "b@p.iam",
				"cloud-controller": "c@p.iam", "gcp-pd-csi": "d@p.iam",
				"image-registry": "e@p.iam", "cloud-network": "f@p.iam",
			},
		}
		iamFile := filepath.Join(dir, "iam.json")
		iamData, err := json.Marshal(iamConfig)
		if err != nil {
			t.Fatalf("marshaling IAM config: %v", err)
		}
		if err := os.WriteFile(iamFile, iamData, 0644); err != nil {
			t.Fatalf("writing IAM config: %v", err)
		}

		netFile := filepath.Join(dir, "net.json")
		if err := os.WriteFile(netFile, []byte(`{"region":"us-east1","networkName":"net","subnetName":"sub"}`), 0644); err != nil {
			t.Fatalf("writing network config: %v", err)
		}

		opts := buildPayloadOptions{
			oidcEndpoint: "https://oidc.test",
		}

		payload, err := buildPayloadFromConfigs("fallback-name", iamFile, netFile, "", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(payload, &result); err != nil {
			t.Fatalf("unmarshaling payload: %v", err)
		}

		spec := result["spec"].(map[string]interface{})
		if spec["infraID"] != "fallback-name" {
			t.Errorf("expected infraID to fall back to cluster name 'fallback-name', got %v", spec["infraID"])
		}
	})
}
