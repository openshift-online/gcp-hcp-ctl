package iam

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	iamapi "google.golang.org/api/iam/v1"

	gcpiam "github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/iam"
)

func TestManagerFormatServiceAccountMethods(t *testing.T) {
	m := &Manager{
		projectID: "test-project",
		infraID:   "test-infra",
		logger:    logr.Discard(),
	}

	tests := []struct {
		name     string
		method   func(string) string
		arg      string
		expected string
	}{
		{
			name:     "When formatServiceAccountID is called it should return correct ID",
			method:   m.formatServiceAccountID,
			arg:      "nodepool-mgmt",
			expected: "test-infra-nodepool-mgmt",
		},
		{
			name:     "When formatServiceAccountEmail is called it should return correct email",
			method:   m.formatServiceAccountEmail,
			arg:      "nodepool-mgmt",
			expected: "test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
		},
		{
			name:     "When formatServiceAccountResource is called it should return correct resource path",
			method:   m.formatServiceAccountResource,
			arg:      "test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
			expected: "projects/test-project/serviceAccounts/test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
		},
		{
			name:     "When formatServiceAccountMember is called it should return correct member format",
			method:   m.formatServiceAccountMember,
			arg:      "test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
			expected: "serviceAccount:test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.method(tt.arg)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestManagerFormatWIFPrincipal(t *testing.T) {
	m := &Manager{
		projectNumber: "123456789",
		infraID:       "test-infra",
		logger:        logr.Discard(),
	}

	tests := []struct {
		name      string
		namespace string
		saName    string
		expected  string
	}{
		{
			name:      "When formatWIFPrincipal is called with kube-system namespace it should return correct principal",
			namespace: "kube-system",
			saName:    "control-plane-operator",
			expected:  "principal://iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/test-infra-wi-pool/subject/system:serviceaccount:kube-system:control-plane-operator",
		},
		{
			name:      "When formatWIFPrincipal is called with custom namespace it should return correct principal",
			namespace: "openshift-cloud-controller-manager",
			saName:    "cloud-controller-manager",
			expected:  "principal://iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/test-infra-wi-pool/subject/system:serviceaccount:openshift-cloud-controller-manager:cloud-controller-manager",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.formatWIFPrincipal(tt.namespace, tt.saName)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestManagerFormatIssuerURI(t *testing.T) {
	tests := []struct {
		name          string
		oidcIssuerURL string
		infraID       string
		expected      string
	}{
		{
			name:          "When custom OIDC issuer URL is set it should return the custom URL",
			oidcIssuerURL: "https://custom-oidc.example.com",
			infraID:       "test-infra",
			expected:      "https://custom-oidc.example.com",
		},
		{
			name:          "When no custom OIDC issuer URL is set it should derive from infraID",
			oidcIssuerURL: "",
			infraID:       "test-infra",
			expected:      "https://hypershift-test-infra-oidc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				oidcIssuerURL: tt.oidcIssuerURL,
				infraID:       tt.infraID,
				logger:        logr.Discard(),
			}
			got := m.formatIssuerURI()
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestAddProjectPolicyMember(t *testing.T) {
	tests := []struct {
		name           string
		policy         *cloudresourcemanager.Policy
		role           string
		member         string
		expectedResult bool
		expectedCount  int
	}{
		{
			name: "When member does not exist in role it should add member and return true",
			policy: &cloudresourcemanager.Policy{
				Bindings: []*cloudresourcemanager.Binding{
					{
						Role:    "roles/compute.admin",
						Members: []string{"serviceAccount:existing@project.iam.gserviceaccount.com"},
					},
				},
			},
			role:           "roles/compute.admin",
			member:         "serviceAccount:new@project.iam.gserviceaccount.com",
			expectedResult: true,
			expectedCount:  2,
		},
		{
			name: "When member already exists in role it should return false",
			policy: &cloudresourcemanager.Policy{
				Bindings: []*cloudresourcemanager.Binding{
					{
						Role:    "roles/compute.admin",
						Members: []string{"serviceAccount:existing@project.iam.gserviceaccount.com"},
					},
				},
			},
			role:           "roles/compute.admin",
			member:         "serviceAccount:existing@project.iam.gserviceaccount.com",
			expectedResult: false,
			expectedCount:  1,
		},
		{
			name: "When role does not exist it should create new binding and return true",
			policy: &cloudresourcemanager.Policy{
				Bindings: []*cloudresourcemanager.Binding{},
			},
			role:           "roles/compute.admin",
			member:         "serviceAccount:new@project.iam.gserviceaccount.com",
			expectedResult: true,
			expectedCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gcpiam.AddProjectPolicyMember(tt.policy, tt.role, tt.member)
			if got != tt.expectedResult {
				t.Errorf("expected %v, got %v", tt.expectedResult, got)
			}
			for _, binding := range tt.policy.Bindings {
				if binding.Role == tt.role {
					if len(binding.Members) != tt.expectedCount {
						t.Errorf("expected %d members, got %d", tt.expectedCount, len(binding.Members))
					}
					break
				}
			}
		})
	}
}

func TestRemoveProjectPolicyMember(t *testing.T) {
	tests := []struct {
		name           string
		policy         *cloudresourcemanager.Policy
		role           string
		member         string
		expectedResult bool
		expectedCount  int
	}{
		{
			name: "When member exists in role it should remove member and return true",
			policy: &cloudresourcemanager.Policy{
				Bindings: []*cloudresourcemanager.Binding{
					{
						Role: "roles/compute.admin",
						Members: []string{
							"serviceAccount:keep@project.iam.gserviceaccount.com",
							"serviceAccount:remove@project.iam.gserviceaccount.com",
						},
					},
				},
			},
			role:           "roles/compute.admin",
			member:         "serviceAccount:remove@project.iam.gserviceaccount.com",
			expectedResult: true,
			expectedCount:  1,
		},
		{
			name: "When member does not exist in role it should return false",
			policy: &cloudresourcemanager.Policy{
				Bindings: []*cloudresourcemanager.Binding{
					{
						Role:    "roles/compute.admin",
						Members: []string{"serviceAccount:existing@project.iam.gserviceaccount.com"},
					},
				},
			},
			role:           "roles/compute.admin",
			member:         "serviceAccount:nonexistent@project.iam.gserviceaccount.com",
			expectedResult: false,
			expectedCount:  1,
		},
		{
			name: "When role does not exist it should return false",
			policy: &cloudresourcemanager.Policy{
				Bindings: []*cloudresourcemanager.Binding{},
			},
			role:           "roles/compute.admin",
			member:         "serviceAccount:any@project.iam.gserviceaccount.com",
			expectedResult: false,
			expectedCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gcpiam.RemoveProjectPolicyMember(tt.policy, tt.role, tt.member)
			if got != tt.expectedResult {
				t.Errorf("expected %v, got %v", tt.expectedResult, got)
			}
			for _, binding := range tt.policy.Bindings {
				if binding.Role == tt.role {
					if len(binding.Members) != tt.expectedCount {
						t.Errorf("expected %d members, got %d", tt.expectedCount, len(binding.Members))
					}
					break
				}
			}
		})
	}
}

func TestAddServiceAccountPolicyMember(t *testing.T) {
	tests := []struct {
		name           string
		policy         *iamapi.Policy
		role           string
		member         string
		expectedResult bool
		expectedCount  int
	}{
		{
			name: "When member does not exist in role it should add member and return true",
			policy: &iamapi.Policy{
				Bindings: []*iamapi.Binding{
					{
						Role:    "roles/iam.workloadIdentityUser",
						Members: []string{"principal://existing"},
					},
				},
			},
			role:           "roles/iam.workloadIdentityUser",
			member:         "principal://new",
			expectedResult: true,
			expectedCount:  2,
		},
		{
			name: "When member already exists in role it should return false",
			policy: &iamapi.Policy{
				Bindings: []*iamapi.Binding{
					{
						Role:    "roles/iam.workloadIdentityUser",
						Members: []string{"principal://existing"},
					},
				},
			},
			role:           "roles/iam.workloadIdentityUser",
			member:         "principal://existing",
			expectedResult: false,
			expectedCount:  1,
		},
		{
			name: "When role does not exist it should create new binding and return true",
			policy: &iamapi.Policy{
				Bindings: []*iamapi.Binding{},
			},
			role:           "roles/iam.workloadIdentityUser",
			member:         "principal://new",
			expectedResult: true,
			expectedCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gcpiam.AddServiceAccountPolicyMember(tt.policy, tt.role, tt.member)
			if got != tt.expectedResult {
				t.Errorf("expected %v, got %v", tt.expectedResult, got)
			}
			for _, binding := range tt.policy.Bindings {
				if binding.Role == tt.role {
					if len(binding.Members) != tt.expectedCount {
						t.Errorf("expected %d members, got %d", tt.expectedCount, len(binding.Members))
					}
					break
				}
			}
		})
	}
}

func TestLoadServiceAccountDefinitions(t *testing.T) {
	t.Run("When loading embedded default configuration it should return valid definitions", func(t *testing.T) {
		definitions, err := LoadServiceAccountDefinitions()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(definitions) == 0 {
			t.Fatal("expected non-empty definitions")
		}

		for _, def := range definitions {
			if def.Name == "" {
				t.Error("expected Name to be non-empty")
			}
			if def.DisplayName == "" {
				t.Errorf("expected DisplayName to be non-empty for %s", def.Name)
			}
		}
	})

	t.Run("When loading cloud-network definition it should have roles populated", func(t *testing.T) {
		definitions, err := LoadServiceAccountDefinitions()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var cloudNetworkDef *ServiceAccountDefinition
		for i := range definitions {
			if definitions[i].Name == "cloud-network" {
				cloudNetworkDef = &definitions[i]
				break
			}
		}
		if cloudNetworkDef == nil {
			t.Fatal("expected to find cloud-network service account definition")
		}
		if len(cloudNetworkDef.Roles) == 0 {
			t.Error("expected cloud-network to have roles")
		}
	})

	t.Run("When loading image-registry definition it should have both operator and server K8s SAs", func(t *testing.T) {
		definitions, err := LoadServiceAccountDefinitions()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var imageRegistryDef *ServiceAccountDefinition
		for i := range definitions {
			if definitions[i].Name == "image-registry" {
				imageRegistryDef = &definitions[i]
				break
			}
		}
		if imageRegistryDef == nil {
			t.Fatal("expected to find image-registry service account definition")
		}
		if len(imageRegistryDef.K8sServiceAccounts) != 2 {
			t.Errorf("expected image-registry to have 2 K8s SA bindings, got %d", len(imageRegistryDef.K8sServiceAccounts))
		}
	})
}

func TestIsTransientIAMError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is nil it should return false",
			err:      nil,
			expected: false,
		},
		{
			name:     "When error is a 429 rate limit error it should return true",
			err:      &googleapi.Error{Code: 429, Message: "A quota has been reached"},
			expected: true,
		},
		{
			name:     "When error is a 404 not found error it should return true",
			err:      &googleapi.Error{Code: 404, Message: "Not found"},
			expected: true,
		},
		{
			name:     "When error is a 403 permission error it should return true",
			err:      &googleapi.Error{Code: 403, Message: "Permission denied"},
			expected: true,
		},
		{
			name:     "When error is a 403 non-permission error it should return false",
			err:      &googleapi.Error{Code: 403, Message: "Forbidden"},
			expected: false,
		},
		{
			name:     "When error is a 500 server error it should return false",
			err:      &googleapi.Error{Code: 500, Message: "Internal server error"},
			expected: false,
		},
		{
			name:     "When error is a non-googleapi error it should return false",
			err:      fmt.Errorf("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientIAMError(tt.err)
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is nil it should return false",
			err:      nil,
			expected: false,
		},
		{
			name:     "When error is a 404 not found it should return true",
			err:      &googleapi.Error{Code: 404, Message: "Not found"},
			expected: true,
		},
		{
			name:     "When error is a 409 conflict it should return false",
			err:      &googleapi.Error{Code: 409, Message: "Already exists"},
			expected: false,
		},
		{
			name:     "When error is a non-googleapi error it should return false",
			err:      fmt.Errorf("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNotFoundError(tt.err)
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestIsAlreadyExistsError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is nil it should return false",
			err:      nil,
			expected: false,
		},
		{
			name:     "When error is a 409 conflict it should return true",
			err:      &googleapi.Error{Code: 409, Message: "Already exists"},
			expected: true,
		},
		{
			name:     "When error is a 404 it should return false",
			err:      &googleapi.Error{Code: 404, Message: "Not found"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAlreadyExistsError(tt.err)
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestLoadAndValidateJWKS(t *testing.T) {
	tests := []struct {
		name          string
		fileContent   string
		setupFile     bool
		expectedError string
		expectedJSON  bool
	}{
		{
			name:         "When valid JWKS file is provided it should return the content",
			fileContent:  `{"keys": [{"kty": "RSA", "use": "sig", "kid": "test-key"}]}`,
			setupFile:    true,
			expectedJSON: true,
		},
		{
			name:          "When file does not exist it should return error",
			setupFile:     false,
			expectedError: "failed to read JWKS file",
		},
		{
			name:          "When file contains invalid JSON it should return error",
			fileContent:   `{not valid json}`,
			setupFile:     true,
			expectedError: "JWKS file contains invalid JSON",
		},
		{
			name:         "When file contains empty JSON object it should return it",
			fileContent:  `{}`,
			setupFile:    true,
			expectedJSON: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var filePath string
			if tt.setupFile {
				tmpDir := t.TempDir()
				filePath = filepath.Join(tmpDir, "jwks.json")
				if err := os.WriteFile(filePath, []byte(tt.fileContent), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
			} else {
				filePath = filepath.Join(t.TempDir(), "non-existent.json")
			}

			result, err := loadAndValidateJWKS(filePath)

			if tt.expectedError != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.expectedError)
					return
				}
				if !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
					return
				}
				if tt.expectedJSON {
					if result != tt.fileContent {
						t.Errorf("expected %q, got %q", tt.fileContent, result)
					}
				}
			}
		})
	}
}

func TestCompareJWKS(t *testing.T) {
	m := &Manager{logger: logr.Discard()}

	tests := []struct {
		name     string
		jwks1    string
		jwks2    string
		expected bool
	}{
		{
			name:     "When both are empty it should return true",
			jwks1:    "",
			jwks2:    "",
			expected: true,
		},
		{
			name:     "When both are whitespace-only it should return true",
			jwks1:    "  ",
			jwks2:    "  \t ",
			expected: true,
		},
		{
			name:     "When first is empty and second is not it should return false",
			jwks1:    "",
			jwks2:    `{"keys": []}`,
			expected: false,
		},
		{
			name:     "When first is non-empty and second is empty it should return false",
			jwks1:    `{"keys": []}`,
			jwks2:    "",
			expected: false,
		},
		{
			name:     "When both contain identical JSON it should return true",
			jwks1:    `{"keys": [{"kty": "RSA"}]}`,
			jwks2:    `{"keys": [{"kty": "RSA"}]}`,
			expected: true,
		},
		{
			name:     "When both contain semantically equal JSON with different formatting it should return true",
			jwks1:    `{"keys":[{"kty":"RSA"}]}`,
			jwks2:    `{ "keys" : [ { "kty" : "RSA" } ] }`,
			expected: true,
		},
		{
			name:     "When JSON content differs it should return false",
			jwks1:    `{"keys": [{"kty": "RSA"}]}`,
			jwks2:    `{"keys": [{"kty": "EC"}]}`,
			expected: false,
		},
		{
			name:     "When first contains invalid JSON it should return false",
			jwks1:    `{not json}`,
			jwks2:    `{"keys": []}`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.compareJWKS(tt.jwks1, tt.jwks2)
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}
