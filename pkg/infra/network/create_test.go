package network

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

func TestCreateOptionsValidateInputs(t *testing.T) {
	tests := []struct {
		name          string
		opts          *CreateOptions
		expectedError string
	}{
		{
			name: "When all required fields are provided it should pass validation",
			opts: &CreateOptions{
				InfraID:   "test-infra",
				ProjectID: "test-project",
				Region:    "us-central1",
			},
		},
		{
			name: "When infra-id is missing it should return error",
			opts: &CreateOptions{
				InfraID:   "",
				ProjectID: "test-project",
				Region:    "us-central1",
			},
			expectedError: "infra-id is required",
		},
		{
			name: "When project is missing it should return error",
			opts: &CreateOptions{
				InfraID:   "test-infra",
				ProjectID: "",
				Region:    "us-central1",
			},
			expectedError: "project is required",
		},
		{
			name: "When region is missing it should return error",
			opts: &CreateOptions{
				InfraID:   "test-infra",
				ProjectID: "test-project",
				Region:    "",
			},
			expectedError: "region is required",
		},
		{
			name: "When all fields are missing it should return infra-id error first",
			opts: &CreateOptions{
				InfraID:   "",
				ProjectID: "",
				Region:    "",
			},
			expectedError: "infra-id is required",
		},
		{
			name: "When vpc-cidr is invalid it should return error",
			opts: &CreateOptions{
				InfraID:   "test-infra",
				ProjectID: "test-project",
				Region:    "us-central1",
				VPCCidr:   "not-a-cidr",
			},
			expectedError: "vpc-cidr must be a valid CIDR",
		},
		{
			name: "When vpc-cidr is valid it should pass validation",
			opts: &CreateOptions{
				InfraID:   "test-infra",
				ProjectID: "test-project",
				Region:    "us-central1",
				VPCCidr:   "10.1.0.0/24",
			},
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

func TestCreateWriteOutput(t *testing.T) {
	tests := []struct {
		name          string
		outputFile    string
		results       *CreateOutput
		expectedError string
		validateJSON  bool
	}{
		{
			name:       "When output file is specified it should write JSON to file",
			outputFile: "output.json",
			results: &CreateOutput{
				Region:           "us-central1",
				ProjectID:        "test-project",
				InfraID:          "test-infra",
				NetworkName:      "test-infra-network",
				NetworkSelfLink:  "https://www.googleapis.com/compute/v1/projects/test-project/global/networks/test-infra-network",
				SubnetName:       "test-infra-subnet",
				SubnetSelfLink:   "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-central1/subnetworks/test-infra-subnet",
				SubnetCIDR:       "10.0.0.0/24",
				RouterName:       "test-infra-router",
				NATName:          "test-infra-nat",
				FirewallRuleName: "test-infra-allow-kubelet",
			},
			validateJSON: true,
		},
		{
			name:       "When output file is in invalid directory it should return error",
			outputFile: filepath.Join("missing", "subdir", "output.json"),
			results: &CreateOutput{
				ProjectID: "test-project",
			},
			expectedError: "cannot create output file",
		},
		{
			name:       "When output file is empty string it should write to stdout without error",
			outputFile: "",
			results: &CreateOutput{
				Region:    "us-central1",
				ProjectID: "test-project",
				InfraID:   "test-infra",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			var outputPath string

			if tt.outputFile != "" && !filepath.IsAbs(tt.outputFile) {
				outputPath = filepath.Join(tmpDir, tt.outputFile)
			} else {
				outputPath = tt.outputFile
			}

			opts := &CreateOptions{
				OutputFile: outputPath,
			}

			logger := logr.Discard()
			err := opts.writeOutput(tt.results, logger)

			if tt.expectedError != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.expectedError)
					return
				}
				if !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("expected no error, got %v", err)
				return
			}

			if tt.validateJSON && outputPath != "" {
				data, err := os.ReadFile(outputPath)
				if err != nil {
					t.Fatalf("failed to read output file: %v", err)
				}

				var output CreateOutput
				if err := json.Unmarshal(data, &output); err != nil {
					t.Errorf("output is not valid JSON: %v", err)
					return
				}

				if output.ProjectID != tt.results.ProjectID {
					t.Errorf("expected ProjectID %q, got %q", tt.results.ProjectID, output.ProjectID)
				}
				if output.InfraID != tt.results.InfraID {
					t.Errorf("expected InfraID %q, got %q", tt.results.InfraID, output.InfraID)
				}
				if output.Region != tt.results.Region {
					t.Errorf("expected Region %q, got %q", tt.results.Region, output.Region)
				}
				if output.NetworkName != tt.results.NetworkName {
					t.Errorf("expected NetworkName %q, got %q", tt.results.NetworkName, output.NetworkName)
				}
				if output.SubnetName != tt.results.SubnetName {
					t.Errorf("expected SubnetName %q, got %q", tt.results.SubnetName, output.SubnetName)
				}
				if output.SubnetCIDR != tt.results.SubnetCIDR {
					t.Errorf("expected SubnetCIDR %q, got %q", tt.results.SubnetCIDR, output.SubnetCIDR)
				}
			}
		})
	}
}

func TestNewCreateCommand(t *testing.T) {
	cmd := NewCreateCommand()

	t.Run("When command is created it should have correct use string", func(t *testing.T) {
		if cmd == nil {
			t.Fatal("expected command to be non-nil")
		}
		if cmd.Use != "create <infra-id>" {
			t.Errorf("expected Use to be %q, got %q", "create <infra-id>", cmd.Use)
		}
	})

	t.Run("When command is created it should have vpc-cidr flag with default", func(t *testing.T) {
		flag := cmd.Flag("vpc-cidr")
		if flag == nil {
			t.Error("expected vpc-cidr flag to be defined")
		} else if flag.DefValue != DefaultSubnetCIDR {
			t.Errorf("expected vpc-cidr default %q, got %q", DefaultSubnetCIDR, flag.DefValue)
		}
	})

	t.Run("When command is created it should have output-file flag", func(t *testing.T) {
		if cmd.Flag("output-file") == nil {
			t.Error("expected output-file flag to be defined")
		}
	})
}
