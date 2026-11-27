package iam

import (
	"context"
	"strings"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
	"google.golang.org/api/googleapi"
)

func TestDestroyOptionsValidateInputs(t *testing.T) {
	tests := []struct {
		name          string
		opts          *DestroyOptions
		expectedError string
	}{
		{
			name: "When all required fields are provided it should pass validation",
			opts: &DestroyOptions{
				InfraID:   "test-infra-id",
				ProjectID: "test-project-id",
			},
		},
		{
			name: "When infra-id is missing it should return error",
			opts: &DestroyOptions{
				InfraID:   "",
				ProjectID: "test-project-id",
			},
			expectedError: "infra-id is required",
		},
		{
			name: "When project is missing it should return error",
			opts: &DestroyOptions{
				InfraID:   "test-infra-id",
				ProjectID: "",
			},
			expectedError: "project is required",
		},
		{
			name: "When both infra-id and project are missing it should return infra-id error first",
			opts: &DestroyOptions{
				InfraID:   "",
				ProjectID: "",
			},
			expectedError: "infra-id is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.ValidateInputs()

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
				}
			}
		})
	}
}

func TestNewDestroyCommand(t *testing.T) {
	cmd := NewDestroyCommand()

	if cmd == nil {
		t.Fatal("expected command to be non-nil")
	}

	if cmd.Use != "destroy <infra-id>" {
		t.Errorf("expected Use to be %q, got %q", "destroy <infra-id>", cmd.Use)
	}

	yesFlag := cmd.Flag("yes")
	if yesFlag == nil {
		t.Error("expected yes flag to be defined")
	}
}

func TestNewCreateCommand(t *testing.T) {
	cmd := NewCreateCommand(&config.Config{})

	if cmd == nil {
		t.Fatal("expected command to be non-nil")
	}

	if cmd.Use != "create <infra-id>" {
		t.Errorf("expected Use to be %q, got %q", "create <infra-id>", cmd.Use)
	}

	jwksFlag := cmd.Flag("oidc-jwks-file")
	if jwksFlag == nil {
		t.Error("expected oidc-jwks-file flag to be defined")
	}

	issuerFlag := cmd.Flag("oidc-issuer-url")
	if issuerFlag == nil {
		t.Error("expected oidc-issuer-url flag to be defined")
	}

	outputFlag := cmd.Flag("output-file")
	if outputFlag == nil {
		t.Error("expected output-file flag to be defined")
	}

	enableIngressFlag := cmd.Flag("enable-ingress")
	if enableIngressFlag == nil {
		t.Fatal("expected enable-ingress flag to be defined")
	}
	if enableIngressFlag.DefValue != "false" {
		t.Errorf("expected enable-ingress to default to false, got %q", enableIngressFlag.DefValue)
	}
}

func TestNewIAMCmd(t *testing.T) {
	cmd := NewIAMCmd(&config.Config{})

	if cmd == nil {
		t.Fatal("expected command to be non-nil")
	}

	if cmd.Use != "iam" {
		t.Errorf("expected Use to be %q, got %q", "iam", cmd.Use)
	}

	if len(cmd.Commands()) != 2 {
		t.Errorf("expected 2 subcommands, got %d", len(cmd.Commands()))
	}
}

func TestDestroyIAM(t *testing.T) {
	const (
		wantProvider = "projects/test-project/locations/global/workloadIdentityPools/test-infra-wi-pool/providers/test-infra-k8s-provider"
		wantPool     = "projects/test-project/locations/global/workloadIdentityPools/test-infra-wi-pool"
	)

	t.Run("When every step succeeds it should remove accounts, provider and pool", func(t *testing.T) {
		client := newFakeIAMClient()

		if err := destroyIAM(context.Background(), newTestManager(client, true)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(client.deletedServiceAccounts) != 7 {
			t.Errorf("expected 7 account deletions, got %d", len(client.deletedServiceAccounts))
		}
		if len(client.deletedProviders) != 1 || client.deletedProviders[0] != wantProvider {
			t.Errorf("expected provider %q to be deleted, got %v", wantProvider, client.deletedProviders)
		}
		if len(client.deletedPools) != 1 || client.deletedPools[0] != wantPool {
			t.Errorf("expected pool %q to be deleted, got %v", wantPool, client.deletedPools)
		}
	})

	t.Run("When role removal fails it should still delete the provider and the pool", func(t *testing.T) {
		client := newFakeIAMClient()
		client.removeRolesErr = &googleapi.Error{Code: 403, Message: "permission denied"}

		err := destroyIAM(context.Background(), newTestManager(client, true))
		if err == nil {
			t.Fatal("expected an error when role removal fails")
		}
		if !strings.Contains(err.Error(), "failed to remove project role bindings") {
			t.Errorf("expected the role-binding failure to be reported, got %v", err)
		}
		if len(client.deletedProviders) != 1 {
			t.Errorf("expected the OIDC provider to be deleted despite the failure, got %v", client.deletedProviders)
		}
		if len(client.deletedPools) != 1 {
			t.Errorf("expected the pool to be deleted despite the failure, got %v", client.deletedPools)
		}
		if len(client.deletedServiceAccounts) != 7 {
			t.Errorf("expected all accounts to be deleted, got %d", len(client.deletedServiceAccounts))
		}
	})

	t.Run("When every step fails it should report all failures together", func(t *testing.T) {
		client := newFakeIAMClient()
		client.removeRolesErr = &googleapi.Error{Code: 403, Message: "permission denied"}
		client.deleteSAErr = func(string) error {
			return &googleapi.Error{Code: 500, Message: "backend error"}
		}
		client.deleteProvErr = &googleapi.Error{Code: 500, Message: "backend error"}
		client.deletePoolErr = &googleapi.Error{Code: 500, Message: "backend error"}

		err := destroyIAM(context.Background(), newTestManager(client, true))
		if err == nil {
			t.Fatal("expected an error when every step fails")
		}
		for _, want := range []string{
			"failed to delete service accounts",
			"failed to delete OIDC provider",
			"failed to delete workload identity pool",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("expected error to contain %q, got %v", want, err)
			}
		}
	})
}
