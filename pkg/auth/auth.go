package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/api/idtoken"
)

// tokenFetcher abstracts credential retrieval so tests can inject fakes.
type tokenFetcher interface {
	FetchIdentityToken(ctx context.Context) (string, error)
	FetchAccountEmail(ctx context.Context) (string, error)
}

// gcloudFetcher retrieves tokens and account info via the gcloud CLI.
type gcloudFetcher struct{}

func (g gcloudFetcher) FetchIdentityToken(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "gcloud", "auth", "print-identity-token")
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		if stderr != "" {
			return "", fmt.Errorf("failed to get identity token: %s", stderr)
		}
		return "", fmt.Errorf("failed to get identity token: %w\n\n"+
			"  Ensure gcloud is installed and authenticated:\n"+
			"    gcloud auth login", err)
	}

	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("gcloud returned an empty identity token")
	}
	return token, nil
}

func (g gcloudFetcher) FetchAccountEmail(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "gcloud", "config", "get-value", "account")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get account email: %w", err)
	}
	email := strings.TrimSpace(string(out))
	if email == "" || email == "(unset)" {
		return "", fmt.Errorf("no active gcloud account")
	}
	return email, nil
}

const (
	// defaultTokenLifetime is the assumed lifetime of a gcloud identity token (1 hour).
	defaultTokenLifetime = 55 * time.Minute
	// tokenRefreshTimeout bounds how long we wait for credential operations
	// (gcloud subprocess or Go SDK token exchange) during a token refresh,
	// preventing a hung process or network call from blocking all callers.
	tokenRefreshTimeout = 15 * time.Second
)

// TokenSource provides Google ID tokens and user identity for API authentication.
// Tokens are cached and refreshed automatically when they expire.
type TokenSource struct {
	mu        sync.Mutex
	token     string
	userEmail string
	expiry    time.Time
	fetcher   tokenFetcher
}

// goSDKFetcher retrieves tokens and account info via the Go GCP SDK.
// Used as a fallback when gcloud is not available in PATH.
// Requires GOOGLE_APPLICATION_CREDENTIALS to point to a WIF external_account
// credential file with a service_account_impersonation_url field.
type goSDKFetcher struct {
	audience string
}

// FetchIdentityToken obtains a Google ID token (JWT) using the Go SDK's
// idtoken package. A fresh idtoken.TokenSource is created on each call;
// caching is handled by the outer TokenSource (55-minute window).
//
// Do NOT hoist idtoken.NewTokenSource to goSDKFetcher construction time —
// errors must surface at Token() call time, not at NewTokenSource() time.
func (g goSDKFetcher) FetchIdentityToken(ctx context.Context) (string, error) {
	// Fail fast with an actionable message if the env var is unset — idtoken
	// would surface this as an opaque "could not find default credentials" error.
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
		return "", fmt.Errorf("failed to obtain identity token (credential setup): " +
			"GOOGLE_APPLICATION_CREDENTIALS is not set\n\n" +
			"  Set it to your WIF credential file path, e.g.:\n" +
			"    export GOOGLE_APPLICATION_CREDENTIALS=/path/to/wif-cred.json")
	}
	ts, err := idtoken.NewTokenSource(ctx, g.audience)
	if err != nil {
		return "", fmt.Errorf("failed to obtain identity token (credential setup): %w\n\n"+
			"  Ensure GOOGLE_APPLICATION_CREDENTIALS points to a valid WIF credential file", err)
	}
	// ts.Token() has no context parameter — apply the caller's deadline via a
	// goroutine so a hung GCP endpoint does not block all callers indefinitely,
	// preserving parity with gcloudFetcher's exec.CommandContext behaviour.
	type result struct {
		tok string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		tok, err := ts.Token()
		if err != nil {
			ch <- result{err: fmt.Errorf("failed to obtain identity token (token exchange): %w\n\n"+
				"  Ensure GOOGLE_APPLICATION_CREDENTIALS points to a valid WIF credential file", err)}
			return
		}
		// idtoken stores the JWT in AccessToken despite the field name.
		// Verified against google.golang.org/api v0.266–v0.293: both the legacy
		// oauth2.ReuseTokenSource path and the new oauth2adapt path set AccessToken
		// to the raw JWT string.
		ch <- result{tok: tok.AccessToken}
	}()
	select {
	case r := <-ch:
		return r.tok, r.err
	case <-ctx.Done():
		return "", fmt.Errorf("identity token fetch timed out: %w", ctx.Err())
	}
}

// FetchAccountEmail extracts the service account email from the WIF
// credential file pointed to by GOOGLE_APPLICATION_CREDENTIALS.
// Makes no network calls — parses service_account_impersonation_url directly.
func (g goSDKFetcher) FetchAccountEmail(_ context.Context) (string, error) {
	credFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if credFile == "" {
		return "", fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS is not set")
	}
	data, err := os.ReadFile(credFile)
	if err != nil {
		return "", fmt.Errorf("reading credential file %s: %w", credFile, err)
	}
	return saEmailFromCredJSON(data)
}

// chooseFetcher returns a gcloudFetcher if gcloud is available in PATH,
// or a goSDKFetcher otherwise. Called once at TokenSource construction time.
func chooseFetcher(audience string) tokenFetcher {
	if _, err := exec.LookPath("gcloud"); err == nil {
		return gcloudFetcher{}
	}
	return goSDKFetcher{audience: audience}
}

// NewTokenSource creates a TokenSource that acquires Google ID tokens.
// The audience is the API endpoint URL used for identity token validation
// (e.g. "https://platform-api.example.com").
//
// If gcloud is found in PATH, tokens are acquired via gcloud (default,
// unchanged behaviour for laptop users). Otherwise, tokens are acquired
// via the Go SDK using GOOGLE_APPLICATION_CREDENTIALS (CI/e2e path).
func NewTokenSource(audience string) *TokenSource {
	return &TokenSource{fetcher: chooseFetcher(audience)}
}

// Token returns a valid Google ID token and the authenticated user's email,
// refreshing both if necessary. The context controls cancellation of any
// underlying gcloud subprocess calls.
func (ts *TokenSource) Token(ctx context.Context) (token, userEmail string, err error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.fetcher == nil {
		panic("bug: TokenSource.fetcher is nil — use NewTokenSource() to construct a TokenSource")
	}

	if ts.token != "" && time.Now().Before(ts.expiry) {
		return ts.token, ts.userEmail, nil
	}

	refreshCtx, cancel := context.WithTimeout(ctx, tokenRefreshTimeout)
	defer cancel()

	newToken, err := ts.fetcher.FetchIdentityToken(refreshCtx)
	if err != nil {
		return "", "", err
	}

	newEmail, err := ts.fetcher.FetchAccountEmail(refreshCtx)
	if err != nil {
		return "", "", err
	}

	ts.token = newToken
	ts.userEmail = newEmail
	ts.expiry = time.Now().Add(defaultTokenLifetime)
	return ts.token, ts.userEmail, nil
}

// saEmailFromCredJSON extracts the service account email from the
// service_account_impersonation_url field of a WIF external_account
// credential JSON blob. This mirrors the extraction done internally by
// google.golang.org/api/idtoken tokenSourceFromBytes.
//
// The URL has the form:
//
//	https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/SA_EMAIL:generateAccessToken
//
// filepath.Base returns "SA_EMAIL:generateAccessToken"; Split on ":" gives the email.
func saEmailFromCredJSON(data []byte) (string, error) {
	var cred struct {
		ServiceAccountImpersonationURL string `json:"service_account_impersonation_url"`
	}
	if err := json.Unmarshal(data, &cred); err != nil {
		return "", fmt.Errorf("parsing credential JSON: %w", err)
	}
	if cred.ServiceAccountImpersonationURL == "" {
		return "", fmt.Errorf("credential has no service_account_impersonation_url — " +
			"direct workload pool principals (without SA impersonation) are not supported")
	}
	base := filepath.Base(cred.ServiceAccountImpersonationURL)
	email := strings.Split(base, ":")[0]
	if !strings.Contains(email, "@") {
		return "", fmt.Errorf("service_account_impersonation_url does not contain a service account email (got segment %q)", email)
	}
	return email, nil
}
