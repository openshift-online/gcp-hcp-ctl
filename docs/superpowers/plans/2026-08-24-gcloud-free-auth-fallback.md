# gcloud-free Auth Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Go SDK fallback to `pkg/auth` so that `gcphcpctl` can obtain identity tokens via `idtoken.NewTokenSource` when `gcloud` is not in PATH, enabling e2e pipelines to run without gcloud.

**Architecture:** `chooseFetcher(audience)` detects gcloud at `TokenSource` construction time via `exec.LookPath`; if absent, a new `goSDKFetcher` uses `google.golang.org/api/idtoken` for tokens and parses `service_account_impersonation_url` from the WIF credential JSON for the SA email. `NewTokenSource` gains one `audience string` parameter. All three call sites are updated. No new dependencies.

**Tech Stack:** Go 1.25, `google.golang.org/api/idtoken` (already in go.mod), `os/exec.LookPath`, standard `encoding/json`, `os`, `path/filepath`, `strings`.

## Global Constraints

- `gcloudFetcher` must be entirely unchanged — laptop users must see zero behaviour difference.
- `NewTokenSource` must never fail at construction time — errors surface only at `Token()` call time.
- Do NOT hoist `idtoken.NewTokenSource(ctx, audience)` to struct construction or `chooseFetcher` — it must be called fresh inside `FetchIdentityToken` on each cache miss.
- `google.golang.org/api/idtoken` is already in `go.mod` — no new module dependencies.
- All existing tests must pass without modification (except the two signature-update call sites noted below).
- Run `make test` after every commit to verify.

---

### Task 1: Add `saEmailFromCredJSON` helper and its tests

This is a pure function with no I/O. Writing it first lets every subsequent task build on a tested, stable foundation.

**Files:**
- Modify: `pkg/auth/auth.go`
- Modify: `pkg/auth/auth_test.go`

**Interfaces:**
- Produces: `saEmailFromCredJSON(data []byte) (string, error)` — unexported, in package `auth`

- [ ] **Step 1: Write the failing tests**

Add to `pkg/auth/auth_test.go` (inside `package auth`):

```go
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
```

Also add `"strings"` to the import block in `auth_test.go` if not already present.

- [ ] **Step 2: Run tests to verify they fail**

```bash
make test
```

Expected: compile error — `saEmailFromCredJSON` undefined.

- [ ] **Step 3: Implement `saEmailFromCredJSON` in `pkg/auth/auth.go`**

Add the following imports to `auth.go` (they will also be needed by later tasks — add them all now to avoid repeated edits):

```go
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
```

Add the helper at the bottom of `auth.go`, after the existing `gcloudFetcher` methods:

```go
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
    return email, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
make test
```

Expected: all tests pass including the 5 new ones.

- [ ] **Step 5: Commit**

```bash
git add pkg/auth/auth.go pkg/auth/auth_test.go
git commit -m "feat(auth): add saEmailFromCredJSON helper for WIF credential parsing"
```

---

### Task 2: Add `goSDKFetcher` and `chooseFetcher`, update `NewTokenSource` signature

**Files:**
- Modify: `pkg/auth/auth.go`

**Interfaces:**
- Consumes: `saEmailFromCredJSON(data []byte) (string, error)` from Task 1
- Produces:
  - `goSDKFetcher` struct implementing `tokenFetcher`
  - `chooseFetcher(audience string) tokenFetcher` — unexported
  - `NewTokenSource(audience string) *TokenSource` — updated signature

- [ ] **Step 1: Add `goSDKFetcher` struct and its two methods**

Add after the `gcloudFetcher` methods and before `saEmailFromCredJSON` in `auth.go`:

```go
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
    ts, err := idtoken.NewTokenSource(ctx, g.audience)
    if err != nil {
        return "", fmt.Errorf("failed to obtain identity token via Go SDK: %w\n\n"+
            "  Ensure GOOGLE_APPLICATION_CREDENTIALS points to a valid WIF credential file", err)
    }
    tok, err := ts.Token()
    if err != nil {
        return "", fmt.Errorf("failed to obtain identity token via Go SDK: %w\n\n"+
            "  Ensure GOOGLE_APPLICATION_CREDENTIALS points to a valid WIF credential file", err)
    }
    // idtoken stores the JWT in AccessToken despite the field name.
    // Verified against google.golang.org/api v0.266–v0.293: both the legacy
    // oauth2.ReuseTokenSource path and the new oauth2adapt path set AccessToken
    // to the raw JWT string.
    return tok.AccessToken, nil
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
```

- [ ] **Step 2: Add `chooseFetcher` and update `NewTokenSource`**

Replace the existing `NewTokenSource` function:

```go
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
```

- [ ] **Step 3: Run tests**

```bash
make test
```

Expected: compile error — `NewTokenSource()` called with no arguments in `auth_test.go` and `platformapi/client_test.go`. That is expected; the call-site fixes come in Task 3 and 4.

Actually — to keep the build green during development, temporarily update `TestNewTokenSource_WhenCreated_ItShouldReturnNonNil` in `auth_test.go` now:

```go
func TestNewTokenSource_WhenCreated_ItShouldReturnNonNil(t *testing.T) {
    ts := NewTokenSource("https://api.example.com")
    if ts == nil {
        t.Fatal("expected non-nil TokenSource")
    }
}
```

Then run:

```bash
make test
```

Expected: only `platformapi/client_test.go` fails to compile. All `pkg/auth` tests pass.

- [ ] **Step 4: Commit**

```bash
git add pkg/auth/auth.go pkg/auth/auth_test.go
git commit -m "feat(auth): add goSDKFetcher and chooseFetcher with gcloud auto-detect"
```

---

### Task 3: Add `chooseFetcher` and `goSDKFetcher` unit tests

**Files:**
- Modify: `pkg/auth/auth_test.go`

**Interfaces:**
- Consumes: `chooseFetcher(audience string) tokenFetcher`, `goSDKFetcher`, `gcloudFetcher` from Task 2

- [ ] **Step 1: Add `chooseFetcher` tests**

Add to `auth_test.go`:

```go
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
```

Also add `"path/filepath"` and `"os"` to the import block in `auth_test.go` if not already present.

- [ ] **Step 2: Add `goSDKFetcher.FetchAccountEmail` tests**

```go
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
```

- [ ] **Step 3: Run tests**

```bash
make test
```

Expected: all `pkg/auth` tests pass. `platformapi/client_test.go` still fails to compile — that's fine, fixed in Task 4.

- [ ] **Step 4: Commit**

```bash
git add pkg/auth/auth_test.go
git commit -m "test(auth): add chooseFetcher, goSDKFetcher, and audience threading tests"
```

---

### Task 4: Update call sites — production and test

Update all three locations outside `pkg/auth` that call `auth.NewTokenSource()`.

**Files:**
- Modify: `pkg/cluster/cmd.go`
- Modify: `pkg/nodepool/cmd.go`
- Modify: `pkg/platformapi/client_test.go`

**Interfaces:**
- Consumes: `NewTokenSource(audience string) *TokenSource` from Task 2

- [ ] **Step 1: Update `pkg/cluster/cmd.go`**

In `pkg/cluster/cmd.go`, find `newClient`:

```go
func newClient(apiEndpoint, project string) (*platformapi.Client, error) {
	return platformapi.NewClient(apiEndpoint, project, auth.NewTokenSource())
}
```

Change to:

```go
func newClient(apiEndpoint, project string) (*platformapi.Client, error) {
	return platformapi.NewClient(apiEndpoint, project, auth.NewTokenSource(apiEndpoint))
}
```

- [ ] **Step 2: Update `pkg/nodepool/cmd.go`**

Find the equivalent `newClient` in `pkg/nodepool/cmd.go`. It will look identical. Apply the same change — pass `apiEndpoint` to `auth.NewTokenSource`:

```go
func newClient(apiEndpoint, project string) (*platformapi.Client, error) {
	return platformapi.NewClient(apiEndpoint, project, auth.NewTokenSource(apiEndpoint))
}
```

- [ ] **Step 3: Update `pkg/platformapi/client_test.go`**

Three call sites in `TestNewClientValidation`. Update each `auth.NewTokenSource()` to `auth.NewTokenSource("https://api.example.com")`:

```go
func TestNewClientValidation(t *testing.T) {
	t.Run("When token source is nil it should return error", func(t *testing.T) {
		_, err := NewClient("https://api.example.com", "my-project", nil)
		// ... unchanged
	})

	t.Run("When project is empty it should return error", func(t *testing.T) {
		_, err := NewClient("https://api.example.com", "", auth.NewTokenSource("https://api.example.com"))
		// ... unchanged
	})

	t.Run("When endpoint is HTTP instead of HTTPS it should return error", func(t *testing.T) {
		_, err := NewClient("http://api.example.com", "my-project", auth.NewTokenSource("https://api.example.com"))
		// ... unchanged
	})

	t.Run("When endpoint is empty it should return error", func(t *testing.T) {
		_, err := NewClient("", "my-project", auth.NewTokenSource("https://api.example.com"))
		// ... unchanged
	})
}
```

Note: The audience passed here is a placeholder (`"https://api.example.com"`) — these tests validate `NewClient` input validation, not token fetching, so the audience value is irrelevant.

- [ ] **Step 4: Run full test suite**

```bash
make test
```

Expected: all tests pass, zero compile errors.

- [ ] **Step 5: Commit**

```bash
git add pkg/cluster/cmd.go pkg/nodepool/cmd.go pkg/platformapi/client_test.go
git commit -m "feat(auth): wire audience through NewTokenSource at all call sites"
```

---

### Task 5: Build verification and final check

- [ ] **Step 1: Clean build**

```bash
make clean && make build
```

Expected: `bin/gcphcpctl` produced with no errors or warnings.

- [ ] **Step 2: Full test suite with race detector**

```bash
make test
```

Expected: all tests pass, no race conditions reported.

- [ ] **Step 3: Smoke-test the binary (gcloud path)**

If gcloud is available on your machine:

```bash
./bin/gcphcpctl --help
```

Expected: help output, no panics or auth errors at startup (auth is lazy — no token fetch occurs until a command runs).

- [ ] **Step 4: Smoke-test the binary (SDK path)**

Set PATH to exclude gcloud and verify the binary still starts:

```bash
PATH=/usr/bin:/bin ./bin/gcphcpctl --help
```

Expected: help output. No auth error at this stage — `chooseFetcher` selects `goSDKFetcher` silently; the error (if `GOOGLE_APPLICATION_CREDENTIALS` unset) only appears when a command actually calls `Token()`.

- [ ] **Step 5: Verify lint**

```bash
make lint
```

Expected: no errors.

- [ ] **Step 6: Final commit if any lint fixes were needed**

```bash
git add -A
git commit -m "fix(auth): address lint findings"
```

Only commit if there were actual changes. Skip this step if lint was clean.
