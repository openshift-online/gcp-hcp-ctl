package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/network"
)

var infraIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*-[0-9a-f]{4}$`)

func TestGenerateCompliantInfraID(t *testing.T) {
	t.Run("When given a simple cluster name it should produce a valid infra ID", func(t *testing.T) {
		id, err := generateCompliantInfraID("mycluster")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(id) > maxInfraIDLength {
			t.Errorf("infra ID %q exceeds max length %d", id, maxInfraIDLength)
		}
		if !infraIDPattern.MatchString(id) {
			t.Errorf("infra ID %q does not match expected pattern", id)
		}
	})

	t.Run("When given a long cluster name it should truncate and stay within max length", func(t *testing.T) {
		id, err := generateCompliantInfraID("this-is-a-very-long-cluster-name-that-exceeds-limits")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(id) > maxInfraIDLength {
			t.Errorf("infra ID %q exceeds max length %d", id, maxInfraIDLength)
		}
		if !infraIDPattern.MatchString(id) {
			t.Errorf("infra ID %q does not match expected pattern", id)
		}
	})

	t.Run("When given uppercase letters it should lowercase them", func(t *testing.T) {
		id, err := generateCompliantInfraID("MyCluster")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !infraIDPattern.MatchString(id) {
			t.Errorf("infra ID %q does not match expected pattern (should be lowercase)", id)
		}
	})

	t.Run("When given special characters it should strip them", func(t *testing.T) {
		id, err := generateCompliantInfraID("my_cluster!@#$%")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !infraIDPattern.MatchString(id) {
			t.Errorf("infra ID %q does not match expected pattern", id)
		}
	})

	t.Run("When name starts with digits it should strip leading digits", func(t *testing.T) {
		id, err := generateCompliantInfraID("123abc")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !infraIDPattern.MatchString(id) {
			t.Errorf("infra ID %q should start with a letter", id)
		}
	})

	t.Run("When name sanitizes to empty it should fall back to 'hc' prefix", func(t *testing.T) {
		id, err := generateCompliantInfraID("!!!")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(id) < 3 || id[:2] != "hc" {
			t.Errorf("expected infra ID to start with 'hc', got %q", id)
		}
	})

	t.Run("When name has trailing hyphens it should trim them", func(t *testing.T) {
		id, err := generateCompliantInfraID("cluster---")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !infraIDPattern.MatchString(id) {
			t.Errorf("infra ID %q should not contain trailing hyphens before suffix", id)
		}
	})

	t.Run("When called twice it should produce different IDs", func(t *testing.T) {
		id1, err := generateCompliantInfraID("cluster")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		id2, err := generateCompliantInfraID("cluster")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id1 == id2 {
			t.Errorf("expected unique IDs, both got %q", id1)
		}
	})
}

func TestValidateInfraID(t *testing.T) {
	t.Run("When given a valid infra ID it should return no error", func(t *testing.T) {
		for _, id := range []string{"abc", "my-infra", "a1b2c3", "a-1-b-2"} {
			if err := validateInfraID(id); err != nil {
				t.Errorf("expected %q to be valid, got error: %v", id, err)
			}
		}
	})

	t.Run("When infra ID is exactly max length it should return no error", func(t *testing.T) {
		id := "abcde-fghij-klm"
		if len(id) != maxInfraIDLength {
			t.Fatalf("test setup: expected length %d, got %d", maxInfraIDLength, len(id))
		}
		if err := validateInfraID(id); err != nil {
			t.Errorf("expected %q to be valid, got error: %v", id, err)
		}
	})

	t.Run("When infra ID exceeds max length by one it should return error", func(t *testing.T) {
		id := "abcde-fghij-klmn"
		if len(id) != maxInfraIDLength+1 {
			t.Fatalf("test setup: expected length %d, got %d", maxInfraIDLength+1, len(id))
		}
		if err := validateInfraID(id); err == nil {
			t.Error("expected error for infra ID exceeding max length")
		}
	})

	t.Run("When infra ID exceeds max length it should return error", func(t *testing.T) {
		err := validateInfraID("this-is-too-long-infra-id")
		if err == nil {
			t.Error("expected error for infra ID exceeding max length")
		}
	})

	t.Run("When infra ID starts with a digit it should return error", func(t *testing.T) {
		err := validateInfraID("1abc")
		if err == nil {
			t.Error("expected error for infra ID starting with a digit")
		}
	})

	t.Run("When infra ID starts with a hyphen it should return error", func(t *testing.T) {
		err := validateInfraID("-abc")
		if err == nil {
			t.Error("expected error for infra ID starting with a hyphen")
		}
	})

	t.Run("When infra ID contains uppercase letters it should return error", func(t *testing.T) {
		err := validateInfraID("MyInfra")
		if err == nil {
			t.Error("expected error for infra ID with uppercase letters")
		}
	})

	t.Run("When infra ID contains special characters it should return error", func(t *testing.T) {
		err := validateInfraID("my_infra")
		if err == nil {
			t.Error("expected error for infra ID with underscores")
		}
	})

	t.Run("When infra ID is empty it should return error", func(t *testing.T) {
		err := validateInfraID("")
		if err == nil {
			t.Error("expected error for empty infra ID")
		}
	})
}

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
			clusterName:    "test-cluster",
			infraID:        "test-cluster",
			projectID:      "my-project",
			region:         "us-central1",
			endpointAccess: "PublicAndPrivate",
			oidcEndpoint:   "https://oidc.example.com",
			version:        "4.22.0",
			channelGroup:   "candidate",
		}

		netOutput := &network.CreateOutput{
			NetworkName: "my-vpc",
			SubnetName:  "my-subnet",
		}
		req, err := assemblePayload(iamOutput, netOutput, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if req.Name != "test-cluster" {
			t.Errorf("expected name 'test-cluster', got %v", req.Name)
		}
		if req.Kind == nil || *req.Kind != "Cluster" {
			t.Errorf("expected kind 'Cluster', got %v", req.Kind)
		}
		if req.Spec.IssuerURL == nil || *req.Spec.IssuerURL != "https://oidc.example.com/test-cluster" {
			t.Errorf("expected issuerURL 'https://oidc.example.com/test-cluster', got %v", req.Spec.IssuerURL)
		}
		if req.Spec.InfraID == nil || *req.Spec.InfraID != "test-cluster" {
			t.Errorf("expected infraID 'test-cluster', got %v", req.Spec.InfraID)
		}
		if req.Spec.Release == nil || req.Spec.Release.Version == nil || *req.Spec.Release.Version != "4.22.0" {
			t.Error("expected version '4.22.0'")
		}
		if req.Spec.Release.ChannelGroup == nil || *req.Spec.Release.ChannelGroup != "candidate" {
			t.Errorf("expected channelGroup 'candidate', got %v", req.Spec.Release.ChannelGroup)
		}
		if req.Spec.Platform.Gcp.ProjectID != "my-project" {
			t.Errorf("expected projectID 'my-project', got %v", req.Spec.Platform.Gcp.ProjectID)
		}
		if req.Spec.Platform.Gcp.Region != "us-central1" {
			t.Errorf("expected region 'us-central1', got %v", req.Spec.Platform.Gcp.Region)
		}
		if req.Spec.Platform.Gcp.Network == nil || *req.Spec.Platform.Gcp.Network != "my-vpc" {
			t.Errorf("expected network 'my-vpc', got %v", req.Spec.Platform.Gcp.Network)
		}
		if req.Spec.Platform.Gcp.Subnet == nil || *req.Spec.Platform.Gcp.Subnet != "my-subnet" {
			t.Errorf("expected subnet 'my-subnet', got %v", req.Spec.Platform.Gcp.Subnet)
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
			clusterName:    "cl",
			infraID:        "cl",
			projectID:      "proj",
			region:         "us-east1",
			endpointAccess: "Private",
			oidcEndpoint:   "https://oidc.test",
		}

		req, err := assemblePayload(iamOutput, netOutput, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if req.Spec.Release != nil {
			t.Error("expected release to be nil when version is empty")
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

		opts := buildPayloadOptions{clusterName: "cl", infraID: "cl", projectID: "proj", region: "us-east1", endpointAccess: "Private", oidcEndpoint: "https://oidc.test"}

		_, err := assemblePayload(iamOutput, netOutput, opts)
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

		opts := buildPayloadOptions{clusterName: "cl", infraID: "cl", projectID: "proj", region: "us-east1", endpointAccess: "Private", oidcEndpoint: "https://oidc.test"}

		_, err := assemblePayload(iamOutput, netOutput, opts)
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
			clusterName:    "my-cluster",
			infraID:        "generated-id",
			region:         "us-central1",
			endpointAccess: "PublicAndPrivate",
			oidcEndpoint:   "https://oidc.test",
			version:        "4.21.0",
		}

		req, err := buildPayloadFromConfigs(iamFile, netFile, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if req.Name != "my-cluster" {
			t.Errorf("expected name 'my-cluster', got %v", req.Name)
		}
		if req.Spec.InfraID == nil || *req.Spec.InfraID != "my-infra" {
			t.Errorf("expected infraID from IAM config 'my-infra' to override generated ID, got %v", req.Spec.InfraID)
		}
		if req.Spec.Platform.Gcp.Region != "europe-west1" {
			t.Errorf("expected region from network config 'europe-west1', got %v", req.Spec.Platform.Gcp.Region)
		}
		if req.Spec.Platform.Gcp.Network == nil || *req.Spec.Platform.Gcp.Network != "net-1" {
			t.Errorf("expected network 'net-1', got %v", req.Spec.Platform.Gcp.Network)
		}
	})

	t.Run("When network config projectId differs from IAM config it should return error", func(t *testing.T) {
		dir := t.TempDir()

		iamConfig := iam.CreateOutput{
			ProjectID:     "project-a",
			ProjectNumber: "111",
			WorkloadIdentityPool: iam.WorkloadIdentityConfig{
				PoolID: "p", ProviderID: "pr",
			},
			ServiceAccounts: map[string]string{
				"ctrlplane-op": "a@p.iam", "nodepool-mgmt": "b@p.iam",
				"cloud-controller": "c@p.iam", "gcp-pd-csi": "d@p.iam",
				"image-registry": "e@p.iam", "cloud-network": "f@p.iam",
			},
		}
		iamFile := filepath.Join(dir, "iam.json")
		iamData, _ := json.Marshal(iamConfig)
		if err := os.WriteFile(iamFile, iamData, 0644); err != nil {
			t.Fatalf("writing IAM config: %v", err)
		}

		netConfig := map[string]string{
			"projectId":   "project-b",
			"region":      "us-east1",
			"networkName": "net",
			"subnetName":  "sub",
		}
		netFile := filepath.Join(dir, "net.json")
		netData, _ := json.Marshal(netConfig)
		if err := os.WriteFile(netFile, netData, 0644); err != nil {
			t.Fatalf("writing network config: %v", err)
		}

		opts := buildPayloadOptions{
			clusterName:  "cl",
			infraID:      "cl",
			projectID:    "project-a",
			region:       "us-east1",
			oidcEndpoint: "https://oidc.test",
		}

		_, err := buildPayloadFromConfigs(iamFile, netFile, opts)
		if err == nil {
			t.Error("expected error for mismatched project IDs")
		}
	})

	t.Run("When IAM config file does not exist it should return error", func(t *testing.T) {
		opts := buildPayloadOptions{clusterName: "cl", infraID: "cl", oidcEndpoint: "https://oidc.test"}
		_, err := buildPayloadFromConfigs("/nonexistent/iam.json", "", opts)
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
			clusterName:  "fallback-name",
			infraID:      "fallback-name",
			region:       "us-central1",
			oidcEndpoint: "https://oidc.test",
		}

		req, err := buildPayloadFromConfigs(iamFile, netFile, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if req.Spec.InfraID == nil || *req.Spec.InfraID != "fallback-name" {
			t.Errorf("expected infraID to fall back to cluster name 'fallback-name', got %v", req.Spec.InfraID)
		}
	})
}
