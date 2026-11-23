package networking

import (
	"context"
	"fmt"
	"google.golang.org/api/compute/v1"
	"strconv"
)

type OperationFailure struct{}

func (*OperationFailure) Error() string    { return "network operation completed with failure" }
func (*OperationFailure) Definitive() bool { return true }

// Start methods return operation identity before waiting, permitting a durable
// write-ahead journal to record the exact operation instead of retrying inserts.
func (c *Client) StartNetwork(ctx context.Context, v *compute.Network) (string, error) {
	op, err := c.service.Networks.Insert(c.projectID, v).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return op.Name, nil
}

func (c *Client) OwnedOperationTarget(ctx context.Context, kind, name string) (string, error) {
	var op *compute.Operation
	var err error
	if kind == "Subnet" || kind == "Router" {
		op, err = c.service.RegionOperations.Get(c.projectID, c.region, name).Context(ctx).Do()
	} else {
		op, err = c.service.GlobalOperations.Get(c.projectID, name).Context(ctx).Do()
	}
	if err != nil {
		return "", err
	}
	if op.Status != "DONE" || op.Error != nil || op.TargetId == 0 {
		return "", fmt.Errorf("operation has no successful target identity")
	}
	return strconv.FormatUint(op.TargetId, 10), nil
}
func (c *Client) StartSubnet(ctx context.Context, v *compute.Subnetwork) (string, error) {
	op, err := c.service.Subnetworks.Insert(c.projectID, c.region, v).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return op.Name, nil
}
func (c *Client) StartRouter(ctx context.Context, v *compute.Router) (string, error) {
	op, err := c.service.Routers.Insert(c.projectID, c.region, v).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return op.Name, nil
}
func (c *Client) StartFirewall(ctx context.Context, v *compute.Firewall) (string, error) {
	op, err := c.service.Firewalls.Insert(c.projectID, v).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return op.Name, nil
}
func (c *Client) WaitOwned(ctx context.Context, kind, operation string) error {
	if kind == "Subnet" || kind == "Router" {
		return c.waitRegionOperation(ctx, operation)
	}
	return c.waitGlobalOperation(ctx, operation)
}

func (c *Client) StartOwnedDelete(ctx context.Context, kind, name string) (string, error) {
	var op *compute.Operation
	var err error
	switch kind {
	case "Network":
		op, err = c.service.Networks.Delete(c.projectID, name).Context(ctx).Do()
	case "Subnet":
		op, err = c.service.Subnetworks.Delete(c.projectID, c.region, name).Context(ctx).Do()
	case "Router":
		op, err = c.service.Routers.Delete(c.projectID, c.region, name).Context(ctx).Do()
	case "Firewall":
		op, err = c.service.Firewalls.Delete(c.projectID, name).Context(ctx).Do()
	default:
		return "", fmt.Errorf("unsupported owned network deletion")
	}
	if err != nil {
		return "", err
	}
	return op.Name, nil
}
