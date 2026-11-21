package platformapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"k8s.io/apimachinery/pkg/types"
)

// PatchClusterIAMApplications tests both immutable UID and the fresh storage
// revision before replacing only the append-only application assertion list.
// The server independently validates that old assertions cannot be removed.
func (c *Client) PatchClusterIAMApplications(ctx context.Context, namespace, name, uid, revision string, applications []v1.WIFConfigTemplateReference) (*v1.Cluster, error) {
	if uid == "" || revision == "" || len(applications) == 0 {
		return nil, fmt.Errorf("exact Cluster UID, resource version and applications are required")
	}
	patch, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": uid},
		{"op": "test", "path": "/metadata/resourceVersion", "value": revision},
		{"op": "replace", "path": "/spec/platform/gcp/iam/templateApplications", "value": applications},
	})
	if err != nil {
		return nil, err
	}
	result := &v1.Cluster{}
	r := c.restClient.Patch(types.JSONPatchType).MaxRetries(0).Namespace(namespace).Resource("clusters").Name(name).Body(patch).Do(ctx)
	if err := decodeResult(r, result, http.MethodPatch, "clusters", name, false); err != nil {
		return nil, err
	}
	return result, nil
}
