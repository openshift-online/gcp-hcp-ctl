package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
)

// ManifestV2 adds the optional stable authorization endpoint to the regional
// endpoints. The global field is omitted by the publisher until routing is ready.
type ManifestV2 struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Environment   string                `json:"environment"`
	Global        GlobalEndpoints       `json:"global"`
	Regions       map[string]RegionInfo `json:"regions"`
}

type GlobalEndpoints struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
}

// AuthorizationInfo is the API endpoint selected for Role and RoleBinding requests.
type AuthorizationInfo struct {
	Endpoint string
}

// ManifestV2URL builds the versioned discovery URL using the same validated
// environment label as the v1 regional manifest.
func ManifestV2URL(environment string) (string, error) {
	v1URL, err := ManifestURL(environment)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(v1URL, "/v1/regions.json") + "/v2/endpoints.json", nil
}

// HTTPStatusError keeps a missing v2 artifact distinguishable from a failed
// fetch or malformed manifest.
type HTTPStatusError struct {
	URL        string
	StatusCode int
}

func (err *HTTPStatusError) Error() string {
	return fmt.Sprintf("fetching discovery manifest %s: HTTP %d", err.URL, err.StatusCode)
}

// FetchV2 retrieves and validates a v2 manifest.
func (fetcher *HTTPFetcher) FetchV2(ctx context.Context, manifestURL string) (*ManifestV2, error) {
	body, err := fetcher.fetchBody(ctx, manifestURL)
	if err != nil {
		return nil, err
	}
	manifest, err := ParseManifestV2(body)
	if err != nil {
		return nil, fmt.Errorf("parsing discovery manifest %s: %w", manifestURL, err)
	}
	return manifest, nil
}

// ParseManifestV2 rejects unsupported schemas and untrusted advertised URLs.
func ParseManifestV2(data []byte) (*ManifestV2, error) {
	var manifest ManifestV2
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if manifest.SchemaVersion != 2 {
		return nil, fmt.Errorf("unsupported discovery schemaVersion %d in v2 manifest (this CLI supports version 2; upgrade gcphcpctl or set --api-endpoint)", manifest.SchemaVersion)
	}
	if endpoint := manifest.Global.AuthorizationEndpoint; endpoint != "" {
		if err := validateAuthorizationEndpoint(endpoint); err != nil {
			return nil, fmt.Errorf("invalid global.authorization_endpoint: %w", err)
		}
	}
	for region, endpoints := range manifest.Regions {
		if endpoints.PlatformAPIEndpoint != "" {
			if err := validateDiscoveredEndpoint(endpoints.PlatformAPIEndpoint); err != nil {
				return nil, fmt.Errorf("region %q has invalid platform_api_endpoint: %w", region, err)
			}
		}
		if endpoints.OIDCIssuer != "" {
			if err := validateDiscoveredEndpoint(endpoints.OIDCIssuer); err != nil {
				return nil, fmt.Errorf("region %q has invalid oidc_issuer: %w", region, err)
			}
		}
	}
	return &manifest, nil
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

// Authorization selects the stable endpoint when advertised. A missing v2
// artifact or global field fails closed for Role and RoleBinding requests;
// explicit API endpoint configuration always takes precedence.
func (resolver *Resolver) Authorization(ctx context.Context, cfg *config.Config) (AuthorizationInfo, error) {
	if cfg.APIEndpoint != "" {
		return AuthorizationInfo{Endpoint: cfg.APIEndpoint}, nil
	}
	if cfg.Environment == "" {
		return AuthorizationInfo{}, fmt.Errorf("global authorization endpoint discovery requires --env or an explicit --api-endpoint")
	}
	v2URL, err := ManifestV2URL(cfg.Environment)
	if err != nil {
		return AuthorizationInfo{}, err
	}
	manifest, err := resolver.cachedManifestV2(ctx, v2URL)
	if err != nil {
		var statusErr *HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			return AuthorizationInfo{}, globalAuthorizationUnavailable(cfg.Environment)
		}
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			parsedURL, _ := url.Parse(v2URL)
			return AuthorizationInfo{}, &discoveryHostNotFoundError{
				environment: cfg.Environment, host: parsedURL.Hostname(), reason: dnsErr.Err, cause: err,
			}
		}
		return AuthorizationInfo{}, err
	}
	if manifest.Global.AuthorizationEndpoint != "" {
		return AuthorizationInfo{Endpoint: manifest.Global.AuthorizationEndpoint}, nil
	}
	return AuthorizationInfo{}, globalAuthorizationUnavailable(cfg.Environment)
}

func globalAuthorizationUnavailable(environment string) error {
	return fmt.Errorf("global authorization endpoint is not configured for env %q; check v2 discovery or set an explicit --api-endpoint", environment)
}

func (resolver *Resolver) cachedManifestV2(ctx context.Context, manifestURL string) (*ManifestV2, error) {
	resolver.cacheMutex.Lock()
	defer resolver.cacheMutex.Unlock()
	if cached, ok := resolver.v2ByURL[manifestURL]; ok {
		return cached, nil
	}
	manifest, err := resolver.fetcher.FetchV2(ctx, manifestURL)
	if err != nil {
		return nil, err
	}
	resolver.v2ByURL[manifestURL] = manifest
	return manifest, nil
}
