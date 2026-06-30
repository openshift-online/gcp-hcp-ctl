package hyperfleet

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/auth"
)

// NewAPIClient creates a HyperFleet API client authenticated via gcloud identity tokens.
// baseURL must use HTTPS (e.g. https://hyperfleet-api.example.com).
func NewAPIClient(baseURL string, tokenSource *auth.TokenSource) (*ClientWithResponses, error) {
	if tokenSource == nil {
		return nil, fmt.Errorf("token source is required")
	}
	if !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("API endpoint must use HTTPS: %s", baseURL)
	}

	baseURL = strings.TrimRight(baseURL, "/")

	injectAuth := func(ctx context.Context, req *http.Request) error {
		token, _, err := tokenSource.Token(ctx)
		if err != nil {
			return fmt.Errorf("obtaining auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}

	return NewClientWithResponses(baseURL, WithRequestEditorFn(injectAuth))
}
