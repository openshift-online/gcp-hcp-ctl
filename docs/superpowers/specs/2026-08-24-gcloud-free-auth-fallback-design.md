# gcloud-free Auth Fallback Design

**Date:** 2026-08-24
**Status:** Approved
**Scope:** `pkg/auth/auth.go`, `pkg/cluster/cmd.go`, `pkg/nodepool/cmd.go`

## Problem

The e2e pipeline runs `gcphcpctl` from a Go-based CI image that does not have `gcloud`
installed. All cluster lifecycle operations (create, get, login, nodepool create, etc.)
currently fail at startup because `pkg/auth` unconditionally shells out to
`gcloud auth print-identity-token` and `gcloud config get-value account`.

The pipeline already sets `GOOGLE_APPLICATION_CREDENTIALS` to a WIF
`external_account` credential file (type `external_account` with
`service_account_impersonation_url`). The Go SDK's `idtoken` package can obtain an
OIDC ID token from that file natively — no gcloud needed.

## Goal

Add a gcloud-free fallback path so that:

- **Laptop users** — no behaviour change; gcloud is found in PATH and used as today.
- **CI without gcloud** — falls back to the Go SDK automatically when gcloud is not in
  PATH and `GOOGLE_APPLICATION_CREDENTIALS` is set.

`pkg/gcp/cloudrun/client.go` already handles this correctly (its `idtoken.NewClient`
first-branch works with WIF) and requires no changes.

## Approach: Auto-detect (gcloud if in PATH, Go SDK otherwise)

`chooseFetcher(audience string)` is called once at `TokenSource` construction time. It
calls `exec.LookPath("gcloud")`:

- **Found** → returns `gcloudFetcher{}` (existing behaviour, unchanged)
- **Not found** → returns `goSDKFetcher{audience: audience}`

No env vars, no config flags, no caller decisions required.

## API Change

`NewTokenSource` gains one parameter:

```go
// Before
func NewTokenSource() *TokenSource

// After
func NewTokenSource(audience string) *TokenSource
```

The `audience` is the Platform API endpoint URL (e.g. `https://platform-api.example.com`).
It is already in scope at both call sites (`pkg/cluster/cmd.go`,
`pkg/nodepool/cmd.go`) as `apiEndpoint`.

Call sites change from:
```go
auth.NewTokenSource()
```
to:
```go
auth.NewTokenSource(apiEndpoint)
```

No other files reference `NewTokenSource`. `hyperfleet` and `platformapi` packages
receive a `*TokenSource` value and are untouched.

## Components

### `chooseFetcher(audience string) tokenFetcher`

Unexported function. `exec.LookPath("gcloud")` decides which fetcher to return.
If gcloud is absent and `GOOGLE_APPLICATION_CREDENTIALS` is unset, returns
`goSDKFetcher` — the token fetch will fail at first `Token()` call with an
actionable error message.

### `goSDKFetcher`

```go
type goSDKFetcher struct {
    audience string
}
```

Implements `tokenFetcher`. Both methods are new; no changes to `gcloudFetcher`.

**`FetchIdentityToken(ctx context.Context) (string, error)`**

Uses `idtoken.NewTokenSource(ctx, audience)` (from `google.golang.org/api/idtoken`,
already in `go.mod`). Construction is lazy — errors surface on first `Token()` call.
Returns `token.AccessToken` (the JWT is carried in this field by the idtoken library).

Error messages:
- Credential file missing / `GOOGLE_APPLICATION_CREDENTIALS` unset →
  `"no GCP credentials found — set GOOGLE_APPLICATION_CREDENTIALS to your WIF credential file"`
- Token fetch failure →
  `"failed to obtain identity token via Go SDK: <err>\n\n  Ensure GOOGLE_APPLICATION_CREDENTIALS points to a valid WIF credential file"`

**`FetchAccountEmail(ctx context.Context) (string, error)`**

Zero network calls. Algorithm:

1. Read `os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")`. If unset → error.
2. `os.ReadFile(credFile)`.
3. JSON-unmarshal only the `service_account_impersonation_url` field into a minimal struct.
4. `filepath.Base(url)` → `"sa@project.iam.gserviceaccount.com:generateAccessToken"`
5. `strings.Split(..., ":")[0]` → `"sa@project.iam.gserviceaccount.com"`

This is the same extraction the `idtoken` library uses internally.

Error cases:
- `GOOGLE_APPLICATION_CREDENTIALS` unset → `"GOOGLE_APPLICATION_CREDENTIALS is not set"`
- File unreadable → `"reading credential file <path>: <err>"`
- `service_account_impersonation_url` absent or empty → `"WIF credential has no service_account_impersonation_url — direct workload pool principals are not supported"`

The extraction logic is factored into an unexported helper:
```go
func saEmailFromCredJSON(data []byte) (string, error)
```
so it can be unit-tested without filesystem or GCP dependencies.

### Token caching

No change. Caching remains entirely in `TokenSource` (55-minute window). `goSDKFetcher`
creates a new `idtoken.TokenSource` on each `FetchIdentityToken` call; this is fine
because `FetchIdentityToken` is only called on cache miss.

## Files Changed

| File | Change |
|---|---|
| `pkg/auth/auth.go` | Add `goSDKFetcher`, `chooseFetcher`, `saEmailFromCredJSON`; update `NewTokenSource` signature |
| `pkg/auth/auth_test.go` | Add tests for `saEmailFromCredJSON`, `chooseFetcher`, and `NewTokenSource` signature; no changes to existing tests |
| `pkg/cluster/cmd.go` | Pass `apiEndpoint` to `auth.NewTokenSource` |
| `pkg/nodepool/cmd.go` | Pass `apiEndpoint` to `auth.NewTokenSource` |
| `pkg/platformapi/client_test.go` | Update three `auth.NewTokenSource()` call sites to pass an audience string |

## No Changes Required

| File | Reason |
|---|---|
| `pkg/gcp/cloudrun/client.go` | `idtoken.NewClient` first-branch already handles WIF; gcloud fallback only triggers for `authorized_user` (laptop) credentials |
| `pkg/platformapi/client.go` | Receives `*auth.TokenSource`, does not construct one |
| `pkg/hyperfleet/hyperfleet.go` | Same |
| `go.mod` / `go.sum` | `google.golang.org/api/idtoken` already imported; no new dependencies |

## Tests

### Existing tests — changes required

`TestNewTokenSource_WhenCreated_ItShouldReturnNonNil` calls `NewTokenSource()` with no
arguments. Update to `NewTokenSource("https://api.example.com")` to match the new
signature. All other existing tests inject `fakeFetcher` directly and are unchanged.

`pkg/platformapi/client_test.go` calls `auth.NewTokenSource()` at three call sites
(lines 23, 33, 43). Update each to `auth.NewTokenSource("https://api.example.com")`.
Test behaviour is unchanged — these tests validate `NewClient` input validation, not
token fetching.

### New tests in `pkg/auth/auth_test.go` (package `auth`, white-box)

**`saEmailFromCredJSON` — pure function, fully unit-testable:**

| Test | Input | Expected |
|---|---|---|
| `TestSAEmailFromCredJSON_ValidURL` | Well-formed `service_account_impersonation_url` ending in `sa@project.iam.gserviceaccount.com:generateAccessToken` | `"sa@project.iam.gserviceaccount.com"`, nil |
| `TestSAEmailFromCredJSON_MissingField` | JSON with no `service_account_impersonation_url` key | error containing "no service_account_impersonation_url" |
| `TestSAEmailFromCredJSON_EmptyURL` | `service_account_impersonation_url` set to `""` | error containing "no service_account_impersonation_url" |
| `TestSAEmailFromCredJSON_InvalidJSON` | Malformed JSON bytes | error |
| `TestSAEmailFromCredJSON_URLWithNoColon` | URL that has no `:generateAccessToken` suffix | returns the full base segment as the email (graceful degradation) |

**`chooseFetcher` — tests the auto-detect logic:**

| Test | Setup | Expected fetcher type |
|---|---|---|
| `TestChooseFetcher_GcloudInPath` | `PATH` contains a directory with a `gcloud` executable stub | `gcloudFetcher` |
| `TestChooseFetcher_GcloudNotInPath` | `PATH` set to an empty temp dir (no gcloud) | `goSDKFetcher` with `audience` field matching the argument |

The gcloud-in-PATH test creates a zero-byte `gcloud` file in a temp dir and prepends
it to `PATH` via `t.Setenv`. The not-in-PATH test sets `PATH` to a fresh empty temp
dir. Both restore `PATH` via `t.Cleanup` automatically.

**`NewTokenSource` signature:**

| Test | Covers |
|---|---|
| `TestNewTokenSource_WhenCreated_ItShouldReturnNonNil` (updated) | Non-nil return with audience argument |
| `TestNewTokenSource_AudiencePassedToGoSDKFetcher` | When gcloud not in PATH, `goSDKFetcher.audience` equals the argument passed to `NewTokenSource` |

**`goSDKFetcher.FetchAccountEmail` — filesystem, no GCP calls:**

These tests use `t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", ...)` and a temp file:

| Test | Setup | Expected |
|---|---|---|
| `TestGoSDKFetcher_FetchAccountEmail_ValidWIF` | Temp file with valid WIF JSON containing `service_account_impersonation_url` | Correct SA email, nil error |
| `TestGoSDKFetcher_FetchAccountEmail_EnvUnset` | `GOOGLE_APPLICATION_CREDENTIALS` unset | Error containing "GOOGLE_APPLICATION_CREDENTIALS is not set" |
| `TestGoSDKFetcher_FetchAccountEmail_FileMissing` | `GOOGLE_APPLICATION_CREDENTIALS` set to a nonexistent path | Error containing the file path |
| `TestGoSDKFetcher_FetchAccountEmail_NoImpersonationURL` | Temp file with WIF JSON missing `service_account_impersonation_url` | Error containing "no service_account_impersonation_url" |

**`goSDKFetcher.FetchIdentityToken` — not unit-tested**

Requires live GCP credentials and a real `idtoken` round-trip. This is the boundary of
unit testing. Covered by the e2e pipeline that motivated this change.

### Test coverage summary

| Component | Unit tested | Notes |
|---|---|---|
| `saEmailFromCredJSON` | Yes — 5 cases | Pure function, no I/O |
| `chooseFetcher` | Yes — 2 cases | Uses `t.Setenv` for PATH manipulation |
| `NewTokenSource` signature | Yes — 2 cases | Updated existing + new audience test |
| `goSDKFetcher.FetchAccountEmail` | Yes — 4 cases | Filesystem only, no GCP calls |
| `goSDKFetcher.FetchIdentityToken` | No | Requires live GCP credentials |
| `TokenSource.Token` caching | Yes — 4 existing cases | Unchanged |
| `platformapi.NewClient` call sites | Yes — 3 updated | Signature-only change, behaviour unchanged |

## Constraints & Risks

- **WIF must have `service_account_impersonation_url`** — direct workload pool principals
  (no SA impersonation) are not supported by `idtoken.NewTokenSource`. The credential
  file used by the CI pipeline has this field. If a future credential type lacks it,
  `FetchAccountEmail` will return a clear error, not a silent empty string.
- **gcloud in CI but broken** — auto-detect picks gcloud if it is in PATH, even if
  misconfigured. This is the correct behaviour (a broken gcloud should surface its own
  error, not be silently skipped).
- **Audience for gcloudFetcher** — gcloud's default identity token (no `--audiences`
  flag) is accepted by the Platform API Gateway. The `audience` parameter is passed to
  `goSDKFetcher` only; `gcloudFetcher` ignores it, preserving existing laptop behaviour.
