package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── fakeFetcher ───────────────────────────────────────────────────────────────

type fakeFetcher struct {
	token     string
	expiry    time.Time
	email     string
	tokenErr  error
	emailErr  error
	callCount int
}

func (f *fakeFetcher) FetchIdentityToken(_ context.Context) (string, time.Time, error) {
	f.callCount++
	return f.token, f.expiry, f.tokenErr
}

func (f *fakeFetcher) FetchAccountEmail(_ context.Context) (string, error) {
	return f.email, f.emailErr
}

// ── TokenSource caching ───────────────────────────────────────────────────────

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
		t.Errorf("expected cached-token, got %q", token)
	}
	if email != "user@example.com" {
		t.Errorf("expected user@example.com, got %q", email)
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
	f := &fakeFetcher{tokenErr: fmt.Errorf("fetch failed")}
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
}

func TestTokenSource_WhenFetcherProvidesExpiry_ItShouldUseThatExpiry(t *testing.T) {
	sdkExpiry := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	f := &fakeFetcher{token: "sdk-token", email: "sa@example.com", expiry: sdkExpiry}
	ts := &TokenSource{fetcher: f}

	_, _, expiry, err := ts.TokenWithExpiry(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !expiry.Equal(sdkExpiry) {
		t.Errorf("expected expiry %v, got %v", sdkExpiry, expiry)
	}
}

func TestTokenSource_WhenFetcherProvidesZeroExpiry_ItShouldUseDefaultLifetime(t *testing.T) {
	f := &fakeFetcher{token: "gcloud-token", email: "user@example.com"} // zero expiry
	ts := &TokenSource{fetcher: f}

	before := time.Now()
	_, _, expiry, err := ts.TokenWithExpiry(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be approximately now + defaultTokenLifetime (55 min).
	lower := before.Add(defaultTokenLifetime - time.Second)
	upper := time.Now().Add(defaultTokenLifetime + time.Second)
	if expiry.Before(lower) || expiry.After(upper) {
		t.Errorf("expected expiry near defaultTokenLifetime, got %v", expiry)
	}
}

// ── chooseFetcher ─────────────────────────────────────────────────────────────

func TestChooseFetcher_WhenNoADCCredentials_ItShouldReturnGcloudFetcher(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/path/creds.json")

	f := chooseFetcher("https://api.example.com")

	if _, ok := f.(gcloudFetcher); !ok {
		t.Errorf("expected gcloudFetcher when no ADC credentials, got %T", f)
	}
}

func TestChooseFetcher_WhenAuthorizedUserCredentials_ItShouldReturnGcloudFetcher(t *testing.T) {
	credFile := writeCredFile(t, `{"type":"authorized_user"}`)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)

	f := chooseFetcher("https://api.example.com")

	if _, ok := f.(gcloudFetcher); !ok {
		t.Errorf("expected gcloudFetcher for authorized_user, got %T", f)
	}
}

func TestChooseFetcher_WhenServiceAccountCredentials_ItShouldReturnGoSDKFetcher(t *testing.T) {
	credFile := writeCredFile(t, `{
		"type": "service_account",
		"client_email": "sa@my-project.iam.gserviceaccount.com"
	}`)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)

	f := chooseFetcher("https://api.example.com")

	sdk, ok := f.(goSDKFetcher)
	if !ok {
		t.Errorf("expected goSDKFetcher for service_account, got %T", f)
	}
	if sdk.audience != "https://api.example.com" {
		t.Errorf("expected audience https://api.example.com, got %q", sdk.audience)
	}
}

func TestChooseFetcher_WhenExternalAccountWithImpersonation_ItShouldReturnGoSDKFetcher(t *testing.T) {
	credFile := writeCredFile(t, `{
		"type": "external_account",
		"audience": "//iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/pool/providers/provider",
		"subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
		"token_url": "https://sts.googleapis.com/v1/token",
		"service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/sa@project.iam.gserviceaccount.com:generateIdToken",
		"credential_source": {"file": "/var/run/secrets/token"}
	}`)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)

	f := chooseFetcher("https://api.example.com")

	if _, ok := f.(goSDKFetcher); !ok {
		t.Errorf("expected goSDKFetcher for external_account, got %T", f)
	}
}

func TestChooseFetcher_WhenExternalAccountBareWIF_ItShouldReturnGoSDKFetcher(t *testing.T) {
	// Bare WIF (no impersonation URL) still gets goSDKFetcher —
	// the error surfaces at token fetch time with an actionable message.
	credFile := writeCredFile(t, `{
		"type": "external_account",
		"audience": "//iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/pool/providers/provider",
		"token_url": "https://sts.googleapis.com/v1/token",
		"credential_source": {"file": "/var/run/secrets/token"}
	}`)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)

	f := chooseFetcher("https://api.example.com")

	if _, ok := f.(goSDKFetcher); !ok {
		t.Errorf("expected goSDKFetcher for bare external_account, got %T", f)
	}
}

// ── saEmailFromCredJSON ───────────────────────────────────────────────────────

func TestSaEmailFromCredJSON_WhenServiceAccount_ItShouldReturnClientEmail(t *testing.T) {
	data := []byte(`{
		"type": "service_account",
		"client_email": "sa@my-project.iam.gserviceaccount.com"
	}`)

	email, err := saEmailFromCredJSON(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if email != "sa@my-project.iam.gserviceaccount.com" {
		t.Errorf("expected sa@my-project.iam.gserviceaccount.com, got %q", email)
	}
}

func TestSaEmailFromCredJSON_WhenServiceAccountMissingEmail_ItShouldReturnError(t *testing.T) {
	data := []byte(`{"type": "service_account"}`)

	_, err := saEmailFromCredJSON(data)
	if err == nil {
		t.Fatal("expected error for missing client_email")
	}
}

func TestSaEmailFromCredJSON_WhenExternalAccountWithImpersonation_ItShouldExtractEmail(t *testing.T) {
	data := []byte(`{
		"type": "external_account",
		"service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/sa@project.iam.gserviceaccount.com:generateIdToken"
	}`)

	email, err := saEmailFromCredJSON(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if email != "sa@project.iam.gserviceaccount.com" {
		t.Errorf("expected sa@project.iam.gserviceaccount.com, got %q", email)
	}
}

func TestSaEmailFromCredJSON_WhenExternalAccountMissingImpersonationURL_ItShouldReturnError(t *testing.T) {
	data := []byte(`{"type": "external_account"}`)

	_, err := saEmailFromCredJSON(data)
	if err == nil {
		t.Fatal("expected error for missing service_account_impersonation_url")
	}
	if !containsAny(err.Error(), "service_account_impersonation_url", "missing") {
		t.Errorf("expected actionable error message, got: %v", err)
	}
}

func TestSaEmailFromCredJSON_WhenUnknownType_ItShouldReturnError(t *testing.T) {
	data := []byte(`{"type": "unknown_type"}`)

	_, err := saEmailFromCredJSON(data)
	if err == nil {
		t.Fatal("expected error for unknown credential type")
	}
}

func TestSaEmailFromCredJSON_WhenInvalidJSON_ItShouldReturnError(t *testing.T) {
	_, err := saEmailFromCredJSON([]byte(`not-json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ── saEmailFromImpersonationURL ───────────────────────────────────────────────

func TestSaEmailFromImpersonationURL_WhenValidURL_ItShouldExtractEmail(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "standard generateIdToken URL",
			url:      "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/sa@project.iam.gserviceaccount.com:generateIdToken",
			expected: "sa@project.iam.gserviceaccount.com",
		},
		{
			name:     "URL without generateIdToken suffix",
			url:      "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/worker@ci-project.iam.gserviceaccount.com",
			expected: "worker@ci-project.iam.gserviceaccount.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			email, err := saEmailFromImpersonationURL(tc.url)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if email != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, email)
			}
		})
	}
}

func TestSaEmailFromImpersonationURL_WhenMissingServiceAccounts_ItShouldReturnError(t *testing.T) {
	_, err := saEmailFromImpersonationURL("https://iamcredentials.googleapis.com/v1/projects/-/noServiceAccountsHere")
	if err == nil {
		t.Fatal("expected error for URL without serviceAccounts/")
	}
}

func TestSaEmailFromImpersonationURL_WhenEmailHasNoAtSign_ItShouldReturnError(t *testing.T) {
	_, err := saEmailFromImpersonationURL("https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/notanemail:generateIdToken")
	if err == nil {
		t.Fatal("expected error for invalid email")
	}
}

// ── readADCFile ───────────────────────────────────────────────────────────────

func TestReadADCFile_WhenGOOGLE_APPLICATION_CREDENTIALSIsSet_ItShouldReadThatFile(t *testing.T) {
	credFile := writeCredFile(t, `{"type":"service_account","client_email":"sa@project.iam.gserviceaccount.com"}`)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)

	data, err := readADCFile()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsAny(string(data), "service_account") {
		t.Error("expected credential file content")
	}
}

func TestReadADCFile_WhenGOOGLE_APPLICATION_CREDENTIALSPointsToMissingFile_ItShouldReturnError(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/credentials.json")

	_, err := readADCFile()
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadADCFile_WhenNoCredentialsConfigured_ItShouldReturnError(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	// Point HOME to a temp dir with no well-known credentials file.
	t.Setenv("HOME", t.TempDir())

	_, err := readADCFile()
	if err == nil {
		t.Fatal("expected error when no credentials are configured")
	}
	if !containsAny(err.Error(), "no ADC credentials", "not configured") {
		t.Errorf("expected actionable error, got: %v", err)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// writeCredFile writes JSON content to a temp file and returns its path.
func writeCredFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "creds-*.json")
	if err != nil {
		t.Fatalf("creating temp credential file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("writing credential file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing credential file: %v", err)
	}
	return filepath.Clean(f.Name())
}

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
