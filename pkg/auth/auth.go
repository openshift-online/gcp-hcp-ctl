package auth

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
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
	// tokenRefreshTimeout bounds how long we wait for gcloud subprocess calls
	// during a token refresh, preventing a hung process from blocking all callers.
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

// NewTokenSource creates a TokenSource that acquires Google ID tokens
// via gcloud without a custom audience (suitable for API Gateway auth).
func NewTokenSource() *TokenSource {
	return &TokenSource{fetcher: gcloudFetcher{}}
}

// Token returns a valid Google ID token and the authenticated user's email,
// refreshing both if necessary. The context controls cancellation of any
// underlying gcloud subprocess calls.
func (ts *TokenSource) Token(ctx context.Context) (token, userEmail string, err error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.fetcher == nil {
		ts.fetcher = gcloudFetcher{}
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
