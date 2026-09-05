package auth

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewAuthCmd(t *testing.T) {
	t.Run("When auth command is created it should be hidden", func(t *testing.T) {
		cmd := NewAuthCmd()
		if !cmd.Hidden {
			t.Error("expected auth command to be hidden")
		}
	})

	t.Run("When auth command is created it should have a token subcommand", func(t *testing.T) {
		cmd := NewAuthCmd()
		found := false
		for _, sub := range cmd.Commands() {
			if sub.Use == "token" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected auth to have a 'token' subcommand")
		}
	})
}

func TestTokenCmdMissingAudience(t *testing.T) {
	t.Run("When --audience is not provided it should return an error", func(t *testing.T) {
		cmd := NewAuthCmd()
		cmd.SetArgs([]string{"token"})
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected error when --audience is missing, got nil")
		}
		if !strings.Contains(err.Error(), "--audience") {
			t.Errorf("error should mention --audience, got: %v", err)
		}
	})
}

func TestExecCredentialJSON(t *testing.T) {
	t.Run("When marshaling execCredential it should produce valid ExecCredential JSON", func(t *testing.T) {
		cred := execCredential{
			APIVersion: "client.authentication.k8s.io/v1beta1",
			Kind:       "ExecCredential",
			Status:     execCredentialStatus{Token: "my-token"},
		}
		data, err := json.Marshal(cred)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if parsed["apiVersion"] != "client.authentication.k8s.io/v1beta1" {
			t.Errorf("got apiVersion %q", parsed["apiVersion"])
		}
		if parsed["kind"] != "ExecCredential" {
			t.Errorf("got kind %q", parsed["kind"])
		}
		status, ok := parsed["status"].(map[string]interface{})
		if !ok {
			t.Fatal("status field missing or wrong type")
		}
		if status["token"] != "my-token" {
			t.Errorf("got token %q, want my-token", status["token"])
		}
		// No expiry set — expirationTimestamp should be absent.
		if _, present := status["expirationTimestamp"]; present {
			t.Error("expirationTimestamp should be absent when expiry is zero")
		}
	})
}

func TestExecCredentialJSON_WithExpiry(t *testing.T) {
	t.Run("When marshaling execCredential with expiry it should include expirationTimestamp", func(t *testing.T) {
		expiry := time.Date(2026, 9, 5, 22, 0, 0, 0, time.UTC)
		cred := execCredential{
			APIVersion: "client.authentication.k8s.io/v1beta1",
			Kind:       "ExecCredential",
			Status: execCredentialStatus{
				Token:               "my-token",
				ExpirationTimestamp: &expiry,
			},
		}
		data, err := json.Marshal(cred)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		status, ok := parsed["status"].(map[string]interface{})
		if !ok {
			t.Fatal("status field missing or wrong type")
		}
		ts, ok := status["expirationTimestamp"].(string)
		if !ok {
			t.Fatalf("expirationTimestamp missing or not a string, got %T", status["expirationTimestamp"])
		}
		parsed2, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			// Try RFC3339Nano (Go's default time.Time JSON format).
			parsed2, err = time.Parse(time.RFC3339Nano, ts)
			if err != nil {
				t.Fatalf("expirationTimestamp %q is not valid RFC3339: %v", ts, err)
			}
		}
		if !parsed2.Equal(expiry) {
			t.Errorf("expirationTimestamp %v does not match expected %v", parsed2, expiry)
		}
	})
}
