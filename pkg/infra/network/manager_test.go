package network

import (
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	"google.golang.org/api/googleapi"
)

func TestManagerFormatMethods(t *testing.T) {
	m := &Manager{
		projectID: "test-project",
		infraID:   "test-infra",
		region:    "us-central1",
		logger:    logr.Discard(),
	}

	tests := []struct {
		name     string
		method   func() string
		expected string
	}{
		{
			name:     "When formatNetworkName is called it should return infraID-network",
			method:   m.formatNetworkName,
			expected: "test-infra-network",
		},
		{
			name:     "When formatSubnetName is called it should return infraID-subnet",
			method:   m.formatSubnetName,
			expected: "test-infra-subnet",
		},
		{
			name:     "When formatRouterName is called it should return infraID-router",
			method:   m.formatRouterName,
			expected: "test-infra-router",
		},
		{
			name:     "When formatNATName is called it should return infraID-nat",
			method:   m.formatNATName,
			expected: "test-infra-nat",
		},
		{
			name:     "When formatFirewallName is called it should return infraID-allow-kubelet",
			method:   m.formatFirewallName,
			expected: "test-infra-allow-kubelet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.method()
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
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
			name:     "When error is a 404 not found it should return false",
			err:      &googleapi.Error{Code: 404, Message: "Not found"},
			expected: false,
		},
		{
			name:     "When error is a 403 forbidden it should return false",
			err:      &googleapi.Error{Code: 403, Message: "Forbidden"},
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
			got := isAlreadyExistsError(tt.err)
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}
