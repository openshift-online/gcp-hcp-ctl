package auth

import (
	"context"
	"fmt"
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
