package platformapi

import (
	"context"
	"net/http"

	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
)

// ControlPlaneUpgradeRequest is the public wire format for a one-time manual
// upgrade request. Keep this small local type until it is available from the
// Gecko platform-api module used by this CLI.
type ControlPlaneUpgradeRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ControlPlaneUpgradeRequestSpec `json:"spec"`
}

type ControlPlaneUpgradeRequestSpec struct {
	ClusterID     string `json:"clusterID"`
	TargetVersion string `json:"targetVersion"`
}

func (r *ControlPlaneUpgradeRequest) DeepCopyObject() runtime.Object {
	copy := *r
	copy.ObjectMeta = *r.ObjectMeta.DeepCopy()
	return &copy
}

// ControlPlaneUpgradeRequestInterface creates requests nested under a Cluster.
type ControlPlaneUpgradeRequestInterface interface {
	Create(ctx context.Context, namespace, clusterName string, request *ControlPlaneUpgradeRequest) (*ControlPlaneUpgradeRequest, error)
}

type controlPlaneUpgradeRequestClient struct {
	restClient rest.Interface
}

func (c *controlPlaneUpgradeRequestClient) Create(ctx context.Context, namespace, clusterName string, request *ControlPlaneUpgradeRequest) (*ControlPlaneUpgradeRequest, error) {
	result := &ControlPlaneUpgradeRequest{}
	response := c.restClient.Post().
		MaxRetries(0).
		Namespace(namespace).
		Resource("clusters").
		Name(clusterName).
		SubResource("controlplaneupgraderequests").
		Body(request).
		Do(ctx)
	if err := decodeResult(response, result, http.MethodPost, "controlplaneupgraderequests", "", true); err != nil {
		return nil, err
	}
	return result, nil
}

func init() {
	scheme.AddKnownTypes(gcpv1.GroupVersion, &ControlPlaneUpgradeRequest{})
}
