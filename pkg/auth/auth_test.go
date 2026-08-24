package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	ts := NewTokenSource("https://api.example.com")
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
	// URL with no ":generateAccessToken" suffix but a valid SA email in the path.
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

func TestSAEmailFromCredJSON_MalformedURL(t *testing.T) {
	// URL where filepath.Base yields a segment with no "@" — should error rather
	// than silently return a non-email string (e.g. "serviceAccounts").
	data := []byte(`{
		"service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/"
	}`)
	_, err := saEmailFromCredJSON(data)
	if err == nil {
		t.Fatal("expected error for malformed URL with no SA email segment")
	}
	if !strings.Contains(err.Error(), "service_account_impersonation_url") {
		t.Errorf("error should mention field, got: %v", err)
	}
}

func TestGoSDKFetcher_FetchIdentityToken_EnvUnset(t *testing.T) {
	// When GOOGLE_APPLICATION_CREDENTIALS is unset, FetchIdentityToken should
	// return a clear actionable error before attempting any GCP network call.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	// Also remove gcloud from PATH so chooseFetcher would select goSDKFetcher.
	t.Setenv("PATH", t.TempDir())

	f := goSDKFetcher{audience: "https://api.example.com"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := f.FetchIdentityToken(ctx)
	if err == nil {
		t.Fatal("expected error when GOOGLE_APPLICATION_CREDENTIALS unset")
	}
	if !strings.Contains(err.Error(), "GOOGLE_APPLICATION_CREDENTIALS") {
		t.Errorf("error should mention GOOGLE_APPLICATION_CREDENTIALS, got: %v", err)
	}
}

func TestChooseFetcher_GcloudNotInPath(t *testing.T) {
	// Point PATH at an empty temp dir so gcloud cannot be found.
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	f := chooseFetcher("https://api.example.com")
	sdk, ok := f.(goSDKFetcher)
	if !ok {
		t.Fatalf("expected goSDKFetcher when gcloud not in PATH, got %T", f)
	}
	if sdk.audience != "https://api.example.com" {
		t.Errorf("expected audience %q, got %q", "https://api.example.com", sdk.audience)
	}
}

func TestChooseFetcher_GcloudInPath(t *testing.T) {
	// Create a stub gcloud executable in a temp dir.
	tmp := t.TempDir()
	gcloudPath := filepath.Join(tmp, "gcloud")
	if err := os.WriteFile(gcloudPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("creating stub gcloud: %v", err)
	}
	t.Setenv("PATH", tmp)

	f := chooseFetcher("https://api.example.com")
	if _, ok := f.(gcloudFetcher); !ok {
		t.Fatalf("expected gcloudFetcher when gcloud in PATH, got %T", f)
	}
}

func TestNewTokenSource_AudienceTrailingSlashStripped(t *testing.T) {
	// Trailing slashes must be stripped so callers using URLs with or without
	// a trailing slash get identical tokens from the Go SDK path.
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	ts := NewTokenSource("https://platform-api.example.com/")
	sdk, ok := ts.fetcher.(goSDKFetcher)
	if !ok {
		t.Fatalf("expected goSDKFetcher, got %T", ts.fetcher)
	}
	if sdk.audience != "https://platform-api.example.com" {
		t.Errorf("expected trailing slash stripped, got audience %q", sdk.audience)
	}
}

func TestNewTokenSource_AudiencePassedToGoSDKFetcher(t *testing.T) {
	// When gcloud is not in PATH, NewTokenSource must thread the audience
	// through to goSDKFetcher.
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	ts := NewTokenSource("https://platform-api.example.com")
	sdk, ok := ts.fetcher.(goSDKFetcher)
	if !ok {
		t.Fatalf("expected goSDKFetcher, got %T", ts.fetcher)
	}
	if sdk.audience != "https://platform-api.example.com" {
		t.Errorf("audience not threaded through: got %q", sdk.audience)
	}
}

func TestGoSDKFetcher_FetchAccountEmail_ValidWIF(t *testing.T) {
	credJSON := []byte(`{
		"type": "external_account",
		"service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/platform-ci-sa@gcp-hcp-ci.iam.gserviceaccount.com:generateAccessToken"
	}`)
	tmp := t.TempDir()
	credFile := filepath.Join(tmp, "wif-cred.json")
	if err := os.WriteFile(credFile, credJSON, 0600); err != nil {
		t.Fatalf("writing cred file: %v", err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)

	f := goSDKFetcher{audience: "https://api.example.com"}
	email, err := f.FetchAccountEmail(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if email != "platform-ci-sa@gcp-hcp-ci.iam.gserviceaccount.com" {
		t.Errorf("got %q", email)
	}
}

func TestGoSDKFetcher_FetchAccountEmail_EnvUnset(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")

	f := goSDKFetcher{audience: "https://api.example.com"}
	_, err := f.FetchAccountEmail(context.Background())
	if err == nil {
		t.Fatal("expected error when GOOGLE_APPLICATION_CREDENTIALS unset")
	}
	if !strings.Contains(err.Error(), "GOOGLE_APPLICATION_CREDENTIALS") {
		t.Errorf("error should mention env var, got: %v", err)
	}
}

func TestGoSDKFetcher_FetchAccountEmail_FileMissing(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/path/wif.json")

	f := goSDKFetcher{audience: "https://api.example.com"}
	_, err := f.FetchAccountEmail(context.Background())
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "/nonexistent/path/wif.json") {
		t.Errorf("error should contain file path, got: %v", err)
	}
}

func TestGoSDKFetcher_FetchAccountEmail_NoImpersonationURL(t *testing.T) {
	credJSON := []byte(`{"type": "external_account"}`)
	tmp := t.TempDir()
	credFile := filepath.Join(tmp, "wif-cred.json")
	if err := os.WriteFile(credFile, credJSON, 0600); err != nil {
		t.Fatalf("writing cred file: %v", err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)

	f := goSDKFetcher{audience: "https://api.example.com"}
	_, err := f.FetchAccountEmail(context.Background())
	if err == nil {
		t.Fatal("expected error for missing service_account_impersonation_url")
	}
	if !strings.Contains(err.Error(), "service_account_impersonation_url") {
		t.Errorf("error should mention field, got: %v", err)
	}
}
