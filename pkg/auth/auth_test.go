package auth

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeFetcher struct {
	token     string
	email     string
	tokenErr  error
	emailErr  error
	callCount int
}

func (f *fakeFetcher) FetchIdentityToken(_ context.Context) (string, error) {
	f.callCount++
	return f.token, f.tokenErr
}

func (f *fakeFetcher) FetchAccountEmail(_ context.Context) (string, error) {
	return f.email, f.emailErr
}

func TestTokenSource_WhenCachedTokenIsValid_ItShouldReturnWithoutFetching(t *testing.T) {
	f := &fakeFetcher{token: "new-token", email: "new@example.com"}
	ts := &TokenSource{
		token:     "cached-token",
		userEmail: "user@example.com",
		expiry:    time.Now().Add(10 * time.Minute),
		fetcher:   f,
	}

	token, email, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "cached-token" {
		t.Errorf("expected cached token, got %q", token)
	}
	if email != "user@example.com" {
		t.Errorf("expected cached email, got %q", email)
	}
	if f.callCount != 0 {
		t.Errorf("expected no fetch calls, got %d", f.callCount)
	}
}

func TestTokenSource_WhenTokenIsExpired_ItShouldRefresh(t *testing.T) {
	f := &fakeFetcher{token: "fresh-token", email: "fresh@example.com"}
	ts := &TokenSource{
		token:   "old-token",
		expiry:  time.Now().Add(-1 * time.Minute),
		fetcher: f,
	}

	token, email, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "fresh-token" {
		t.Errorf("expected fresh-token, got %q", token)
	}
	if email != "fresh@example.com" {
		t.Errorf("expected fresh@example.com, got %q", email)
	}
	if f.callCount != 1 {
		t.Errorf("expected 1 fetch call, got %d", f.callCount)
	}
}

func TestTokenSource_WhenTokenFetchFails_ItShouldNotCorruptCache(t *testing.T) {
	f := &fakeFetcher{tokenErr: fmt.Errorf("gcloud not found")}
	ts := &TokenSource{
		token:     "original-token",
		userEmail: "original@example.com",
		expiry:    time.Now().Add(-1 * time.Minute),
		fetcher:   f,
	}

	_, _, err := ts.Token(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if ts.token != "original-token" {
		t.Errorf("expected cache unchanged, got token %q", ts.token)
	}
	if ts.userEmail != "original@example.com" {
		t.Errorf("expected cache unchanged, got email %q", ts.userEmail)
	}
}

func TestTokenSource_WhenEmailFetchFails_ItShouldNotCorruptCache(t *testing.T) {
	f := &fakeFetcher{token: "new-token", emailErr: fmt.Errorf("no account")}
	ts := &TokenSource{
		token:     "original-token",
		userEmail: "original@example.com",
		expiry:    time.Now().Add(-1 * time.Minute),
		fetcher:   f,
	}

	_, _, err := ts.Token(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if ts.token != "original-token" {
		t.Errorf("expected cache unchanged, got token %q", ts.token)
	}
	if ts.userEmail != "original@example.com" {
		t.Errorf("expected cache unchanged, got email %q", ts.userEmail)
	}
}

func TestNewTokenSource_WhenCreated_ItShouldReturnNonNil(t *testing.T) {
	ts := NewTokenSource()
	if ts == nil {
		t.Fatal("expected non-nil TokenSource")
	}
}

func TestSAEmailFromCredJSON_ValidURL(t *testing.T) {
	data := []byte(`{
		"type": "external_account",
		"service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/platform-ci-sa@gcp-hcp-ci.iam.gserviceaccount.com:generateAccessToken"
	}`)
	got, err := saEmailFromCredJSON(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "platform-ci-sa@gcp-hcp-ci.iam.gserviceaccount.com" {
		t.Errorf("got %q, want %q", got, "platform-ci-sa@gcp-hcp-ci.iam.gserviceaccount.com")
	}
}

func TestSAEmailFromCredJSON_MissingField(t *testing.T) {
	data := []byte(`{"type": "external_account"}`)
	_, err := saEmailFromCredJSON(data)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "service_account_impersonation_url") {
		t.Errorf("error should mention field name, got: %v", err)
	}
}

func TestSAEmailFromCredJSON_EmptyURL(t *testing.T) {
	data := []byte(`{"service_account_impersonation_url": ""}`)
	_, err := saEmailFromCredJSON(data)
	if err == nil {
		t.Fatal("expected error for empty URL, got nil")
	}
	if !strings.Contains(err.Error(), "service_account_impersonation_url") {
		t.Errorf("error should mention field name, got: %v", err)
	}
}

func TestSAEmailFromCredJSON_InvalidJSON(t *testing.T) {
	_, err := saEmailFromCredJSON([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestSAEmailFromCredJSON_URLWithNoColon(t *testing.T) {
	// If the URL has no ":generateAccessToken" suffix, Split returns the full
	// base segment — we accept it as a best-effort email.
	data := []byte(`{
		"service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/plain-sa@project.iam.gserviceaccount.com"
	}`)
	got, err := saEmailFromCredJSON(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "plain-sa@project.iam.gserviceaccount.com" {
		t.Errorf("got %q", got)
	}
}
