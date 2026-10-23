package platformapi

import (
	"context"
	"encoding/json"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// NodePoolUpgradeRequest is the public API's one-time NodePool upgrade request.
// Keep this wire type local until the Gecko revision that defines it is
// available as an upstream platform-api module dependency.
type NodePoolUpgradeRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              NodePoolUpgradeRequestSpec    `json:"spec"`
	Status            *NodePoolUpgradeRequestStatus `json:"status,omitempty"`
}

type NodePoolUpgradeRequestSpec struct {
	ClusterID     string       `json:"clusterID"`
	NodePoolID    string       `json:"nodePoolID"`
	TargetVersion string       `json:"targetVersion"`
	StartTime     *metav1.Time `json:"startTime,omitempty"`
}

type NodePoolUpgradeRequestStatus struct {
	Conditions    []metav1.Condition `json:"conditions,omitempty"`
	AcceptedTime  *metav1.Time       `json:"acceptedTime,omitempty"`
	StartedTime   *metav1.Time       `json:"startedTime,omitempty"`
	CompletedTime *metav1.Time       `json:"completedTime,omitempty"`
}

// NodePoolUpgradeRequests returns the nested Cluster request API.
func (c *Client) NodePoolUpgradeRequests() NodePoolUpgradeRequestInterface {
	return &nodePoolUpgradeRequestClient{restClient: c.restClient}
}

type NodePoolUpgradeRequestInterface interface {
	Create(ctx context.Context, namespace, clusterName string, request *NodePoolUpgradeRequest) (*NodePoolUpgradeRequest, error)
	Get(ctx context.Context, namespace, clusterName, name string) (*NodePoolUpgradeRequest, error)
}

type nodePoolUpgradeRequestClient struct {
	restClient rest.Interface
}

func (c *nodePoolUpgradeRequestClient) Create(ctx context.Context, namespace, clusterName string,
	request *NodePoolUpgradeRequest) (*NodePoolUpgradeRequest, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	response := c.restClient.Post().MaxRetries(0).
		Namespace(namespace).Resource("clusters").Name(clusterName).
		SubResource("nodepoolupgraderequests").Body(body).Do(ctx)
	return decodeNodePoolUpgradeRequest(response, http.MethodPost, "", true)
}

func (c *nodePoolUpgradeRequestClient) Get(ctx context.Context, namespace, clusterName, name string) (*NodePoolUpgradeRequest, error) {
	response := c.restClient.Get().Namespace(namespace).Resource("clusters").Name(clusterName).
		SubResource("nodepoolupgraderequests", name).Do(ctx)
	return decodeNodePoolUpgradeRequest(response, http.MethodGet, name, false)
}

func decodeNodePoolUpgradeRequest(response rest.Result, method, name string, nonIdempotent bool) (*NodePoolUpgradeRequest, error) {
	if err := normalizeResult(response, method, "nodepoolupgraderequests", name, nonIdempotent); err != nil {
		return nil, err
	}
	body, err := response.Raw()
	if err != nil {
		return nil, invalidNodePoolUpgradeResponse(method, nonIdempotent)
	}
	var request NodePoolUpgradeRequest
	if json.Unmarshal(body, &request) != nil || request.Kind != "NodePoolUpgradeRequest" || request.Name == "" {
		return nil, invalidNodePoolUpgradeResponse(method, nonIdempotent)
	}
	return &request, nil
}

func invalidNodePoolUpgradeResponse(method string, nonIdempotent bool) error {
	message := "invalid response"
	if method != http.MethodGet {
		message = "server reported success, but its response was invalid"
	}
	err := &responseDecodeError{message: message}
	if nonIdempotent {
		return &uncertainOutcomeError{cause: err}
	}
	return err
}
