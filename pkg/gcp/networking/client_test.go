package networking

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

func TestWrapAuthError(t *testing.T) {
	tests := []struct {
		name           string
		action         string
		err            error
		wantContains   string
		wantUnwrapped  bool
	}{
		{
			name:          "When credentials are not found it should return guidance to login",
			action:        "creating client",
			err:           fmt.Errorf("could not find default credentials"),
			wantContains:  "gcloud auth application-default login",
			wantUnwrapped: true,
		},
		{
			name:          "When token has expired it should return guidance to re-login",
			action:        "inserting network",
			err:           fmt.Errorf("oauth2: token expired"),
			wantContains:  "credentials have expired",
			wantUnwrapped: true,
		},
		{
			name:          "When token expired without oauth2 prefix it should still match",
			action:        "getting router",
			err:           fmt.Errorf("token expired and refresh failed"),
			wantContains:  "credentials have expired",
			wantUnwrapped: true,
		},
		{
			name:          "When permission is denied via googleapi.Error it should return API enablement guidance",
			action:        "inserting firewall",
			err:           &googleapi.Error{Code: 403, Message: "forbidden"},
			wantContains:  "permission denied",
			wantUnwrapped: true,
		},
		{
			name:          "When permission is denied via string it should still match",
			action:        "deleting subnet",
			err:           fmt.Errorf("PermissionDenied: caller does not have permission"),
			wantContains:  "permission denied",
			wantUnwrapped: true,
		},
		{
			name:          "When an unknown error occurs it should wrap the original error",
			action:        "patching router",
			err:           fmt.Errorf("connection refused"),
			wantContains:  "patching router",
			wantUnwrapped: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wrapAuthError(tt.action, tt.err)
			if result == nil {
				t.Fatal("expected non-nil error")
			}
			msg := result.Error()
			if !strings.Contains(msg, tt.wantContains) {
				t.Errorf("expected error to contain %q, got %q", tt.wantContains, msg)
			}
			if !strings.Contains(msg, tt.action) {
				t.Errorf("expected error to contain action %q, got %q", tt.action, msg)
			}
			if tt.wantUnwrapped {
				if errors.Unwrap(result) == nil {
					t.Errorf("expected error to preserve chain via %%w, but Unwrap returned nil")
				}
			}
		})
	}
}

func TestIsPermissionDeniedError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "When error is googleapi 403 it should return true",
			err:  &googleapi.Error{Code: 403, Message: "forbidden"},
			want: true,
		},
		{
			name: "When error is googleapi 404 it should return false",
			err:  &googleapi.Error{Code: 404, Message: "not found"},
			want: false,
		},
		{
			name: "When error contains PermissionDenied string it should return true",
			err:  fmt.Errorf("PermissionDenied: no access"),
			want: true,
		},
		{
			name: "When error contains permission denied lowercase it should return true",
			err:  fmt.Errorf("permission denied for resource"),
			want: true,
		},
		{
			name: "When error is unrelated it should return false",
			err:  fmt.Errorf("connection timeout"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPermissionDeniedError(tt.err); got != tt.want {
				t.Errorf("isPermissionDeniedError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatOperationErrors(t *testing.T) {
	tests := []struct {
		name   string
		errors []*compute.OperationErrorErrors
		want   string
	}{
		{
			name:   "When errors is empty it should return unknown error",
			errors: nil,
			want:   "unknown error",
		},
		{
			name: "When there is a single error it should format code and message",
			errors: []*compute.OperationErrorErrors{
				{Code: "QUOTA_EXCEEDED", Message: "out of quota"},
			},
			want: "QUOTA_EXCEEDED: out of quota",
		},
		{
			name: "When there are multiple errors it should join with semicolons",
			errors: []*compute.OperationErrorErrors{
				{Code: "ERR1", Message: "first"},
				{Code: "ERR2", Message: "second"},
			},
			want: "ERR1: first; ERR2: second",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatOperationErrors(tt.errors)
			if got != tt.want {
				t.Errorf("formatOperationErrors() = %q, want %q", got, tt.want)
			}
		})
	}
}

