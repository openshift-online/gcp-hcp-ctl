package discovery

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
)

func TestParseManifestV2(t *testing.T) {
	valid := []byte(`{"schemaVersion":2,"environment":"dev","global":{"authorization_endpoint":"https://authz.ck1104.dev.gcp-hcp.devshift.net"},"regions":{"us-central1":{"platform_api_endpoint":"https://api-c1.dev.gcp-hcp.devshift.net","oidc_issuer":"https://oidc-c1.dev.gcp-hcp.devshift.net"}}}`)
	manifest, err := ParseManifestV2(valid)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Global.AuthorizationEndpoint != "https://authz.ck1104.dev.gcp-hcp.devshift.net" {
		t.Fatal(manifest.Global)
	}
	for _, input := range []string{
		`{"schemaVersion":3,"regions":{}}`,
		`{"schemaVersion":2,"global":{"authorization_endpoint":"http://authz.dev.gcp-hcp.devshift.net"}}`,
		`{"schemaVersion":2,"global":{"authorization_endpoint":"https://attacker.example.com"}}`,
		`{"schemaVersion":2,"global":{"authorization_endpoint":"https://authz.int.gcp-hcp.devshift.net/other"}}`,
		`{"schemaVersion":2,"global":{"authorization_endpoint":"https://authz.int.gcp-hcp.devshift.net?token=secret"}}`,
		`{"schemaVersion":2,"regions":{"us-central1":{"platform_api_endpoint":"https://attacker.example.com"}}}`,
	} {
		if _, err := ParseManifestV2([]byte(input)); err == nil {
			t.Errorf("accepted invalid manifest %s", input)
		}
	}
	if _, err := ParseManifestV2([]byte(`{"schemaVersion":3}`)); err == nil || !strings.Contains(err.Error(), "unsupported discovery schemaVersion 3") {
		t.Fatalf("unsupported schema error: %v", err)
	}
}

func TestAuthorizationResolution(t *testing.T) {
	v2URL, err := ManifestV2URL("integration")
	if err != nil {
		t.Fatal(err)
	}
	if v2URL != "https://discovery.int.gcp-hcp.devshift.net/v2/endpoints.json" {
		t.Fatal(v2URL)
	}
	v1URL, _ := ManifestURL("integration")
	fetcher := &fakeFetcher{
		v2ByURL: map[string]*ManifestV2{v2URL: {
			SchemaVersion: 2,
			Global:        GlobalEndpoints{AuthorizationEndpoint: "https://authz.int.gcp-hcp.devshift.net"},
			Regions: map[string]RegionInfo{
				"us-central1": {PlatformAPIEndpoint: "https://api-c1.int.gcp-hcp.devshift.net"},
			},
		}},
		manifestsByURL: map[string]*Manifest{v1URL: manifestWith("us-central1", "https://api-c1.int.gcp-hcp.devshift.net", "")},
	}
	resolver := newResolver(fetcher)
	ctx := context.Background()
	global, err := resolver.Authorization(ctx, &config.Config{Environment: "integration"})
	if err != nil || global.Endpoint != "https://authz.int.gcp-hcp.devshift.net" {
		t.Fatalf("global without region: %+v, %v", global, err)
	}
	// A promotion changes regional data in a freshly published manifest but
	// leaves the customer-facing URL unchanged.
	promoted := &fakeFetcher{v2ByURL: map[string]*ManifestV2{v2URL: {
		SchemaVersion: 2,
		Global:        GlobalEndpoints{AuthorizationEndpoint: global.Endpoint},
		Regions: map[string]RegionInfo{
			"us-central1": {PlatformAPIEndpoint: "https://new-leader.int.gcp-hcp.devshift.net"},
		},
	}}}
	again, err := newResolver(promoted).Authorization(ctx, &config.Config{Environment: "integration", Region: "us-central1"})
	if err != nil || again != global {
		t.Fatalf("endpoint changed after promotion: %+v, %v", again, err)
	}
	_, err = resolver.Authorization(ctx, &config.Config{Environment: "integration", Region: "us-central1"})
	if err != nil {
		t.Fatal(err)
	}
	if calls := fetcher.callsByURL[v2URL]; calls != 1 {
		t.Fatalf("expected cached v2 manifest, got %d fetches", calls)
	}
	// Explicit endpoints bypass discovery, including its schema errors.
	overrideFetcher := &fakeFetcher{err: errors.New("unsupported schema")}
	override, err := newResolver(overrideFetcher).Authorization(ctx, &config.Config{Environment: "integration", APIEndpoint: "https://explicit.example.com"})
	if err != nil || override.Endpoint != "https://explicit.example.com" {
		t.Fatalf("override: %+v, %v", override, err)
	}
	if len(overrideFetcher.callsByURL) != 0 {
		t.Fatalf("explicit endpoint still fetched discovery: %v", overrideFetcher.callsByURL)
	}

	delete(fetcher.v2ByURL, v2URL)
	resolver = newResolver(fetcher)
	regional, err := resolver.Authorization(ctx, &config.Config{Environment: "integration", Region: "us-central1"})
	if err != nil || regional.Endpoint != "https://api-c1.int.gcp-hcp.devshift.net" {
		t.Fatalf("v1 fallback: %+v, %v", regional, err)
	}
	if calls := fetcher.callsByURL[v1URL]; calls != 1 {
		t.Fatalf("expected one v1 fetch, got %d", calls)
	}
	missingRegion, err := resolver.Authorization(ctx, &config.Config{Environment: "integration"})
	if err != nil || missingRegion.Endpoint != "" {
		t.Fatalf("missing region: %+v, %v", missingRegion, err)
	}

	fetcher.v2ByURL[v2URL] = &ManifestV2{SchemaVersion: 2, Regions: map[string]RegionInfo{
		"us-central1": {PlatformAPIEndpoint: "https://api-c1.int.gcp-hcp.devshift.net"},
	}}
	resolver = newResolver(fetcher)
	regional, err = resolver.Authorization(ctx, &config.Config{Environment: "integration", Region: "us-central1"})
	if err != nil || regional.Endpoint != "https://api-c1.int.gcp-hcp.devshift.net" {
		t.Fatalf("v2 omitted global: %+v, %v", regional, err)
	}
	_, err = resolver.Authorization(ctx, &config.Config{Environment: "integration", Region: "us-east4"})
	if err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("unknown region: %v", err)
	}
	fetcher.v2ByURL[v2URL].Regions["us-central1"] = RegionInfo{}
	resolver = newResolver(fetcher)
	_, err = resolver.Authorization(ctx, &config.Config{Environment: "integration", Region: "us-central1"})
	if err == nil || !strings.Contains(err.Error(), "no platform_api_endpoint") {
		t.Fatalf("missing regional endpoint: %v", err)
	}
}

func TestAuthorizationDoesNotFallbackOnDiscoveryFailure(t *testing.T) {
	for _, failure := range []error{
		&HTTPStatusError{URL: "https://discovery.int.gcp-hcp.devshift.net/v2/endpoints.json", StatusCode: http.StatusServiceUnavailable},
		errors.New("unsupported discovery schemaVersion 3"),
	} {
		fetcher := &fakeFetcher{err: failure}
		_, err := newResolver(fetcher).Authorization(context.Background(), &config.Config{Environment: "integration", Region: "us-central1"})
		if !errors.Is(err, failure) || len(fetcher.callsByURL) != 1 {
			t.Fatalf("failure %v was hidden: %v, calls: %v", failure, err, fetcher.callsByURL)
		}
	}
}
