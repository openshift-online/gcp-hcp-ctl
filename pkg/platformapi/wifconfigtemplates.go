package platformapi

import (
	"context"
	"fmt"
	"net/http"

	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"k8s.io/client-go/rest"
)

// WIFConfigTemplateInterface intentionally exposes only authenticated catalog reads.
type WIFConfigTemplateInterface interface {
	Get(context.Context, string) (*gcpv1.WIFConfigTemplate, error)
	List(context.Context) (*gcpv1.WIFConfigTemplateList, error)
}

func (c *Client) WIFConfigTemplates() WIFConfigTemplateInterface {
	return &wifConfigTemplateClient{c.restClient}
}

type wifConfigTemplateClient struct{ restClient rest.Interface }

func (c *wifConfigTemplateClient) Get(ctx context.Context, name string) (*gcpv1.WIFConfigTemplate, error) {
	result := &gcpv1.WIFConfigTemplate{}
	if err := decodeResult(c.restClient.Get().Resource("wifconfigtemplates").Name(name).Do(ctx), result, http.MethodGet, "wifconfigtemplates", name, false); err != nil {
		return nil, err
	}
	return result, nil
}
func (c *wifConfigTemplateClient) List(ctx context.Context) (*gcpv1.WIFConfigTemplateList, error) {
	result := &gcpv1.WIFConfigTemplateList{}
	token := ""
	seen := map[string]bool{}
	for {
		page := &gcpv1.WIFConfigTemplateList{}
		req := c.restClient.Get().Resource("wifconfigtemplates")
		if token != "" {
			req = req.Param("continue", token)
		}
		if err := decodeResult(req.Do(ctx), page, http.MethodGet, "wifconfigtemplates", "", false); err != nil {
			return nil, err
		}
		result.Items = append(result.Items, page.Items...)
		token = page.Continue
		if token == "" {
			break
		}
		if seen[token] {
			return nil, fmt.Errorf("listing templates: repeated pagination token")
		}
		seen[token] = true
	}
	return result, nil
}
