package network

import (
	"strings"
	"testing"
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
				InfraID:   "test-infra",
				ProjectID: "test-project",
				Region:    "us-central1",
			},
		},
		{
			name: "When infra-id is missing it should return error",
			opts: &DestroyOptions{
				InfraID:   "",
				ProjectID: "test-project",
				Region:    "us-central1",
			},
			expectedError: "infra-id is required",
		},
		{
			name: "When project is missing it should return error",
			opts: &DestroyOptions{
				InfraID:   "test-infra",
				ProjectID: "",
				Region:    "us-central1",
			},
			expectedError: "project is required",
		},
		{
			name: "When region is missing it should return error",
			opts: &DestroyOptions{
				InfraID:   "test-infra",
				ProjectID: "test-project",
				Region:    "",
			},
			expectedError: "region is required",
		},
		{
			name: "When all fields are missing it should return infra-id error first",
			opts: &DestroyOptions{
				InfraID:   "",
				ProjectID: "",
				Region:    "",
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

	t.Run("When command is created it should have correct use string", func(t *testing.T) {
		if cmd == nil {
			t.Fatal("expected command to be non-nil")
		}
		if cmd.Use != "destroy <infra-id>" {
			t.Errorf("expected Use to be %q, got %q", "destroy <infra-id>", cmd.Use)
		}
	})

	t.Run("When command is created it should have yes flag", func(t *testing.T) {
		if cmd.Flag("yes") == nil {
			t.Error("expected yes flag to be defined")
		}
	})
}

func TestNewNetworkCmd(t *testing.T) {
	cmd := NewNetworkCmd()

	if cmd == nil {
		t.Fatal("expected command to be non-nil")
	}

	if cmd.Use != "network" {
		t.Errorf("expected Use to be %q, got %q", "network", cmd.Use)
	}

	if len(cmd.Commands()) != 2 {
		t.Errorf("expected 2 subcommands, got %d", len(cmd.Commands()))
	}
}
