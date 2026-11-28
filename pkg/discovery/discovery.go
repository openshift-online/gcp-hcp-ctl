// Package discovery resolves regional and global endpoints from the static
// endpoint-discovery manifest published per environment (see gcp-hcp-infra GCP-1147):
//
//	https://discovery.{env}.gcp-hcp.devshift.net/v2/endpoints.json
//
// Environments without v2 temporarily use /v1/regions.json when v2 returns 404.
//
// {env} is a short label: a known environment name maps to its abbreviation
// (integration -> int), while any other value — a shared dev sector, an
// ephemeral CI run — is used verbatim. That lets one --env value target any
// discovery manifest under gcp-hcp.devshift.net without a separate URL override.
//
// The manifest is advisory, not authoritative: it tells a client which endpoints
// to try, but the platform API server still enforces access. Callers that
// already know their endpoints bypass discovery by supplying explicit endpoints
// (flags, environment variables, or the config file).
package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/httpdebug"
)

const (
	manifestVersionV1 = 1
	manifestVersionV2 = 2
)

// defaultFetchTimeout bounds a single manifest fetch. Discovery sits on the
// cluster-create critical path, so a hung DNS/CDN lookup must fail fast rather
// than block the command indefinitely.
const defaultFetchTimeout = 10 * time.Second

// maxManifestBytes caps the manifest body read so a hostile or misbehaving
// origin cannot stream an unbounded response into memory.
const maxManifestBytes = 1 << 20

// discoveryDomain is the trusted parent domain for both the manifest URL and
// every endpoint the manifest advertises. It is fixed so that neither an --env
// value nor a manifest entry can redirect the CLI off this domain.
const discoveryDomain = "gcp-hcp.devshift.net"

// Kind selects which endpoint of a region entry to resolve.
type Kind int

const (
	// KindAPI resolves the platform API endpoint (platform_api_endpoint).
	KindAPI Kind = iota
	// KindOIDC resolves the OIDC issuer base URL (oidc_issuer).
	KindOIDC
)

func (kind Kind) String() string {
	switch kind {
	case KindAPI:
		return "platform API endpoint"
	case KindOIDC:
		return "OIDC issuer"
	default:
		return "unknown endpoint"
	}
}

// RegionInfo is a single region entry in the discovery manifest.
type RegionInfo struct {
	OIDCIssuer          string `json:"oidc_issuer" yaml:"oidc_issuer"`
	PlatformAPIEndpoint string `json:"platform_api_endpoint" yaml:"platform_api_endpoint"`
}

// Manifest holds endpoints from either discovery version. SchemaVersion is
// normalized after parsing because v1 uses schema_version:"v1" and v2 uses
// schemaVersion:2. Keys of Regions are public GCP region names.
type Manifest struct {
	SchemaVersion int                   `json:"-"`
	Environment   string                `json:"environment"`
	Regions       map[string]RegionInfo `json:"regions"`
	Global        GlobalEndpoints       `json:"global"`
}

type GlobalEndpoints struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
}

// Fetcher retrieves and parses a discovery manifest from a URL. It is an
// interface so tests can supply a manifest without network access.
type Fetcher interface {
	Fetch(ctx context.Context, manifestURL string, version int) (*Manifest, error)
}

// knownEnvLabels maps friendly environment names (and their common synonyms) to
// the short label used in the discovery hostname. These mirror the stable
// environments in gcp-hcp-infra's terraform/metadata/environments.yaml.
var knownEnvLabels = map[string]string{
	"dev":         "dev",
	"integration": "int",
	"int":         "int",
	"stage":       "stg",
	"staging":     "stg",
	"stg":         "stg",
	"production":  "prd",
	"prod":        "prd",
	"prd":         "prd",
}

// envLabelPattern matches one or more dot-separated DNS labels (lowercase
// alphanumeric and hyphens). It bounds a pass-through --env value so it can only
// extend the discovery hostname — never alter the scheme, the domain suffix, or
// the path.
var envLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// envLabel resolves an --env value to its discovery subdomain label. A known
// environment name maps to its short form; any other value is used verbatim, so
// a shared dev sector or an ephemeral CI run is reachable with a single --env:
//
//	integration          -> discovery.int.gcp-hcp.devshift.net
//	<infra-id>.dev       -> discovery.<infra-id>.dev.gcp-hcp.devshift.net
//	<run-id>.platform-ci -> discovery.<run-id>.platform-ci.gcp-hcp.devshift.net
func envLabel(environment string) string {
	normalized := strings.ToLower(strings.TrimSpace(environment))
	if label, ok := knownEnvLabels[normalized]; ok {
		return label
	}
	return normalized
}

// manifestBaseURL validates an --env value and returns its trusted HTTPS origin.
func manifestBaseURL(environment string) (string, error) {
	label := envLabel(environment)
	if label == "" {
		return "", fmt.Errorf("env is required to build the discovery manifest URL")
	}
	if !envLabelPattern.MatchString(label) {
		return "", fmt.Errorf("invalid env %q: must be an environment name or a discovery subdomain label", environment)
	}
	return fmt.Sprintf("https://discovery.%s.%s", label, discoveryDomain), nil
}

// ManifestURL builds the v1 discovery URL for temporary compatibility.
func ManifestURL(environment string) (string, error) {
	baseURL, err := manifestBaseURL(environment)
	if err != nil {
		return "", err
	}
	return baseURL + "/v1/regions.json", nil
}

// ManifestV2URL builds the primary discovery URL for an --env value.
func ManifestV2URL(environment string) (string, error) {
	baseURL, err := manifestBaseURL(environment)
	if err != nil {
		return "", err
	}
	return baseURL + "/v2/endpoints.json", nil
}

// EndpointRequiredError explains how to supply the endpoint a command needs.
// With an environment selected, the caller needs a region to discover a missing
// endpoint, or it can supply that endpoint explicitly. With no environment,
// discovery cannot run, so the message offers both paths.
func EndpointRequiredError(kind Kind, environment string) error {
	endpointFlag := "--api-endpoint"
	if kind == KindOIDC {
		endpointFlag = "--oidc-endpoint"
	}
	if environment != "" {
		return fmt.Errorf("--region is required for endpoint discovery with --env %q (set --region, GCPHCPCTL_REGION, or region in config; or use %s to bypass discovery)", environment, endpointFlag)
	}
	endpointHint := "GCPHCPCTL_API_ENDPOINT or api_endpoint in config"
	if kind == KindOIDC {
		endpointHint = "GCPHCPCTL_OIDC_ENDPOINT or oidc_endpoint in config"
	}
	return fmt.Errorf("no endpoint available: set --env and --region for discovery (or GCPHCPCTL_ENVIRONMENT / GCPHCPCTL_REGION or config), or set an explicit endpoint via %s, %s", endpointFlag, endpointHint)
}

// Resolver resolves endpoints via endpoint discovery, caching the selected
// document so regional and global lookups share the same fetch result.
type Resolver struct {
	fetcher Fetcher

	cacheMutex       sync.Mutex
	manifestsByV2URL map[string]*Manifest
}

// NewResolver returns a Resolver that fetches discovery manifests over HTTPS.
func NewResolver() *Resolver {
	return newResolver(NewHTTPFetcher(defaultFetchTimeout))
}

// newResolver builds a Resolver around a specific Fetcher. Tests use it to inject
// a fetcher that serves canned manifests without a network call.
func newResolver(fetcher Fetcher) *Resolver {
	return &Resolver{
		fetcher:          fetcher,
		manifestsByV2URL: make(map[string]*Manifest),
	}
}

// Region returns the configured and discovered endpoints for a region. Explicit
// values from cfg take precedence; the manifest supplies missing values.
//
// With no environment or no region, it returns the available explicit values
// without fetching; the caller raises EndpointRequiredError if one is needed.
func (resolver *Resolver) Region(ctx context.Context, cfg *config.Config) (RegionInfo, error) {
	info := RegionInfo{PlatformAPIEndpoint: cfg.APIEndpoint, OIDCIssuer: cfg.OIDCEndpoint}
	if info.PlatformAPIEndpoint != "" && info.OIDCIssuer != "" {
		return info, nil
	}
	if cfg.Environment == "" || cfg.Region == "" {
		return info, nil
	}

	manifest, err := resolver.FetchManifest(ctx, cfg.Environment)
	if err != nil {
		return RegionInfo{}, err
	}
	entry, found := manifest.Regions[cfg.Region]
	if !found {
		// Point the user at the single actionable next step — the command that
		// lists valid regions — rather than inlining the region list.
		return RegionInfo{}, fmt.Errorf("region %q is not available in env %q; run 'gcphcpctl regions list --env %s' to see available regions",
			cfg.Region, cfg.Environment, cfg.Environment)
	}
	if info.PlatformAPIEndpoint == "" {
		info.PlatformAPIEndpoint = entry.PlatformAPIEndpoint
	}
	if info.OIDCIssuer == "" {
		info.OIDCIssuer = entry.OIDCIssuer
	}
	return info, nil
}

// AuthorizationInfo is the API endpoint selected for Role and RoleBinding requests.
type AuthorizationInfo struct {
	Endpoint string
}

// Authorization selects the stable endpoint from the shared discovery manifest.
// A present v2 artifact without the global field is a configuration error. A
// missing v2 artifact uses v1 regional discovery during rollout. An explicit
// API endpoint always takes precedence.
func (resolver *Resolver) Authorization(ctx context.Context, cfg *config.Config) (AuthorizationInfo, error) {
	if cfg.APIEndpoint != "" {
		return AuthorizationInfo{Endpoint: cfg.APIEndpoint}, nil
	}
	if cfg.Environment == "" {
		return AuthorizationInfo{}, fmt.Errorf("global authorization endpoint discovery requires --env or an explicit --api-endpoint")
	}
	manifest, err := resolver.FetchManifest(ctx, cfg.Environment)
	if err != nil {
		return AuthorizationInfo{}, err
	}
	if manifest.SchemaVersion == manifestVersionV2 {
		if manifest.Global.AuthorizationEndpoint == "" {
			return AuthorizationInfo{}, fmt.Errorf("global authorization endpoint is not configured for env %q; check v2 discovery or set an explicit --api-endpoint", cfg.Environment)
		}
		return AuthorizationInfo{Endpoint: manifest.Global.AuthorizationEndpoint}, nil
	}
	if cfg.Region == "" {
		return AuthorizationInfo{}, fmt.Errorf("v2 discovery is not available for env %q; set --region to use v1 regional discovery or set --api-endpoint", cfg.Environment)
	}
	info, err := resolver.Region(ctx, cfg)
	if err != nil {
		return AuthorizationInfo{}, err
	}
	if info.PlatformAPIEndpoint == "" {
		return AuthorizationInfo{}, fmt.Errorf("region %q in env %q has no platform_api_endpoint in v1 discovery; check the manifest or set --api-endpoint", cfg.Region, cfg.Environment)
	}
	return AuthorizationInfo{Endpoint: info.PlatformAPIEndpoint}, nil
}

// FetchManifest loads v2 for every discovery consumer. Only a v2 404 selects
// v1; malformed v2, other HTTP errors, and network failures are returned.
// Successful results, including a v1 fallback, are cached per environment URL.
func (resolver *Resolver) FetchManifest(ctx context.Context, environment string) (*Manifest, error) {
	v2URL, err := ManifestV2URL(environment)
	if err != nil {
		return nil, err
	}
	resolver.cacheMutex.Lock()
	defer resolver.cacheMutex.Unlock()
	if cached, ok := resolver.manifestsByV2URL[v2URL]; ok {
		return cached, nil
	}

	manifest, err := resolver.fetcher.Fetch(ctx, v2URL, manifestVersionV2)
	if err != nil {
		var statusErr *HTTPStatusError
		if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound {
			return nil, formatDiscoveryError(environment, v2URL, err)
		}
		// Temporary compatibility while v2 is being published across environments.
		v1URL, _ := ManifestURL(environment) // v2URL already validated the environment.
		manifest, err = resolver.fetcher.Fetch(ctx, v1URL, manifestVersionV1)
		if err != nil {
			return nil, formatDiscoveryError(environment, v1URL, err)
		}
	}
	resolver.manifestsByV2URL[v2URL] = manifest
	return manifest, nil
}

func formatDiscoveryError(environment, manifestURL string, err error) error {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		parsedURL, _ := url.Parse(manifestURL) // Manifest URLs are validated before fetching.
		return &discoveryHostNotFoundError{
			environment: environment,
			host:        parsedURL.Hostname(),
			reason:      dnsErr.Err,
			cause:       err,
		}
	}
	return err
}

// discoveryHostNotFoundError keeps the DNS cause available to callers while
// showing a concise, actionable message at the CLI.
type discoveryHostNotFoundError struct {
	environment string
	host        string
	reason      string
	cause       error
}

func (err *discoveryHostNotFoundError) Error() string {
	message := fmt.Sprintf("Could not load endpoint discovery for environment %q.\n  Host: %s\n  Cause: DNS lookup returned %q.\n\nCheck the environment name and whether its discovery DNS record is available.", err.environment, err.host, err.reason)
	if envLabel(err.environment) == "dev" {
		message += "\nFor shared dev environments, use --env <infra-id>.dev."
	}
	return message
}

func (err *discoveryHostNotFoundError) Unwrap() error { return err.cause }

// HTTPFetcher fetches a discovery manifest over HTTPS. The manifest is public
// (unauthenticated), so no credentials are attached.
type HTTPFetcher struct {
	httpClient *http.Client
}

// NewHTTPFetcher returns an HTTPFetcher. A zero timeout uses the default.
func NewHTTPFetcher(timeout time.Duration) *HTTPFetcher {
	if timeout <= 0 {
		timeout = defaultFetchTimeout
	}
	return &HTTPFetcher{httpClient: &http.Client{
		Transport: httpdebug.Wrap(nil),
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// Fetch retrieves a manifest and checks the schema expected at its URL.
func (fetcher *HTTPFetcher) Fetch(ctx context.Context, manifestURL string, version int) (*Manifest, error) {
	body, err := fetcher.fetchBody(ctx, manifestURL)
	if err != nil {
		return nil, err
	}
	manifest, err := ParseManifest(body, version)
	if err != nil {
		return nil, fmt.Errorf("parsing discovery manifest %s: %w", manifestURL, err)
	}
	return manifest, nil
}

// HTTPStatusError distinguishes a missing v2 artifact from other fetch errors.
type HTTPStatusError struct {
	URL        string
	StatusCode int
}

func (err *HTTPStatusError) Error() string {
	return fmt.Sprintf("fetching discovery manifest %s: HTTP %d", err.URL, err.StatusCode)
}

func (fetcher *HTTPFetcher) fetchBody(ctx context.Context, manifestURL string) ([]byte, error) {
	if err := validateHTTPSURL(manifestURL); err != nil {
		return nil, fmt.Errorf("invalid discovery manifest URL: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating discovery request for %s: %w", manifestURL, err)
	}

	response, err := fetcher.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetching discovery manifest %s: %w", manifestURL, err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{URL: manifestURL, StatusCode: response.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading discovery manifest %s: %w", manifestURL, err)
	}
	if len(body) > maxManifestBytes {
		return nil, fmt.Errorf("discovery manifest %s exceeds %d bytes", manifestURL, maxManifestBytes)
	}

	return body, nil
}

// ParseManifest decodes either wire format into one Manifest. The expected
// version comes from the URL being fetched; a mismatched document is rejected.
// Every advertised endpoint must be HTTPS under the trusted discovery domain.
func ParseManifest(data []byte, expectedVersion int) (*Manifest, error) {
	var header struct {
		V1 string `json:"schema_version"`
		V2 int    `json:"schemaVersion"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, err
	}
	switch expectedVersion {
	case manifestVersionV1:
		if header.V1 != "v1" {
			return nil, fmt.Errorf("unsupported manifest schema_version %q (want %q)", header.V1, "v1")
		}
	case manifestVersionV2:
		if header.V2 != manifestVersionV2 {
			return nil, fmt.Errorf("unsupported discovery schemaVersion %d in v2 manifest (this CLI supports version 2; upgrade gcphcpctl or set --api-endpoint)", header.V2)
		}
	default:
		return nil, fmt.Errorf("unsupported discovery manifest version %d", expectedVersion)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	manifest.SchemaVersion = expectedVersion
	if expectedVersion == manifestVersionV2 {
		if endpoint := manifest.Global.AuthorizationEndpoint; endpoint != "" {
			if err := validateAuthorizationEndpoint(endpoint); err != nil {
				return nil, fmt.Errorf("invalid global.authorization_endpoint: %w", err)
			}
		}
	} else {
		// A v1 document cannot advertise a global authorization endpoint.
		manifest.Global = GlobalEndpoints{}
	}
	if err := validateRegions(manifest.Regions); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func validateRegions(regions map[string]RegionInfo) error {
	for region, endpoints := range regions {
		if endpoints.PlatformAPIEndpoint != "" {
			if err := validateDiscoveredEndpoint(endpoints.PlatformAPIEndpoint); err != nil {
				return fmt.Errorf("region %q has invalid platform_api_endpoint: %w", region, err)
			}
		}
		if endpoints.OIDCIssuer != "" {
			if err := validateDiscoveredEndpoint(endpoints.OIDCIssuer); err != nil {
				return fmt.Errorf("region %q has invalid oidc_issuer: %w", region, err)
			}
		}
	}
	return nil
}

// A global authorization endpoint is a base URL. Reject path and query values
// before the REST client appends its API path and attaches an identity token.
func validateAuthorizationEndpoint(endpoint string) error {
	if err := validateDiscoveredEndpoint(endpoint); err != nil {
		return err
	}
	parsed, _ := url.Parse(endpoint)
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.ForceQuery {
		return fmt.Errorf("must not include a path or query")
	}
	return nil
}

// validateHTTPSURL requires a well-formed HTTPS URL with a host and no embedded
// credentials or fragment.
func validateHTTPSURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("must be an HTTPS URL with a host and no credentials or fragment")
	}
	return nil
}

// validateDiscoveredEndpoint ensures a manifest-advertised endpoint is HTTPS and
// a subdomain of (or equal to) the trusted discovery domain.
func validateDiscoveredEndpoint(rawURL string) error {
	if err := validateHTTPSURL(rawURL); err != nil {
		return err
	}
	parsed, _ := url.Parse(rawURL)
	host := strings.ToLower(parsed.Hostname())
	if host != discoveryDomain && !strings.HasSuffix(host, "."+discoveryDomain) {
		return fmt.Errorf("host must be under %s", discoveryDomain)
	}
	return nil
}
