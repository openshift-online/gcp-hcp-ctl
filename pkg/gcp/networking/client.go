// Package networking provides a client for managing GCP network resources
// including VPC networks, subnets, routers (including NAT configuration),
// and firewall rules.
package networking

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	operationTimeout      = 5 * time.Minute
	operationPollInterval = 2 * time.Second
)

func wrapAuthError(action string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "could not find default credentials"):
		return fmt.Errorf("%s: no GCP credentials found\n\n"+
			"  Run: gcloud auth application-default login\n"+
			"  Or set GOOGLE_APPLICATION_CREDENTIALS to a service account key file: %w", action, err)
	case strings.Contains(msg, "token expired") || strings.Contains(msg, "oauth2: token expired"):
		return fmt.Errorf("%s: GCP credentials have expired\n\n"+
			"  Run: gcloud auth application-default login: %w", action, err)
	case isPermissionDeniedError(err):
		return fmt.Errorf("%s: permission denied\n\n"+
			"  Ensure the Compute Engine API is enabled: gcloud services enable compute.googleapis.com --project <project>: %w", action, err)
	default:
		return fmt.Errorf("%s: %w", action, err)
	}
}

func isPermissionDeniedError(err error) bool {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) && apiErr.Code == 403 {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "PermissionDenied") || strings.Contains(msg, "permission denied")
}

// Client wraps the GCP Compute Engine API for network operations.
type Client struct {
	projectID string
	region    string
	service   *compute.Service
}

// NewClient creates a new networking client using Application Default Credentials.
func NewClient(ctx context.Context, projectID, region string) (*Client, error) {
	if projectID == "" {
		return nil, fmt.Errorf("projectID is required")
	}
	if region == "" {
		return nil, fmt.Errorf("region is required")
	}

	service, err := compute.NewService(ctx, option.WithScopes(compute.CloudPlatformScope))
	if err != nil {
		return nil, wrapAuthError("creating networking client", err)
	}
	return &Client{
		projectID: projectID,
		region:    region,
		service:   service,
	}, nil
}

// ============================================================================
// VPC Networks
// ============================================================================

func (c *Client) InsertNetwork(ctx context.Context, network *compute.Network) error {
	op, err := c.service.Networks.Insert(c.projectID, network).Context(ctx).Do()
	if err != nil {
		return wrapAuthError("inserting network", err)
	}
	return c.waitGlobalOperation(ctx, op.Name)
}

func (c *Client) GetNetwork(ctx context.Context, name string) (*compute.Network, error) {
	net, err := c.service.Networks.Get(c.projectID, name).Context(ctx).Do()
	if err != nil {
		return nil, wrapAuthError("getting network", err)
	}
	return net, nil
}

func (c *Client) DeleteNetwork(ctx context.Context, name string) error {
	op, err := c.service.Networks.Delete(c.projectID, name).Context(ctx).Do()
	if err != nil {
		return wrapAuthError("deleting network", err)
	}
	return c.waitGlobalOperation(ctx, op.Name)
}

// ============================================================================
// Subnets
// ============================================================================

func (c *Client) InsertSubnet(ctx context.Context, subnet *compute.Subnetwork) error {
	op, err := c.service.Subnetworks.Insert(c.projectID, c.region, subnet).Context(ctx).Do()
	if err != nil {
		return wrapAuthError("inserting subnet", err)
	}
	return c.waitRegionOperation(ctx, op.Name)
}

func (c *Client) GetSubnet(ctx context.Context, name string) (*compute.Subnetwork, error) {
	sub, err := c.service.Subnetworks.Get(c.projectID, c.region, name).Context(ctx).Do()
	if err != nil {
		return nil, wrapAuthError("getting subnet", err)
	}
	return sub, nil
}

func (c *Client) DeleteSubnet(ctx context.Context, name string) error {
	op, err := c.service.Subnetworks.Delete(c.projectID, c.region, name).Context(ctx).Do()
	if err != nil {
		return wrapAuthError("deleting subnet", err)
	}
	return c.waitRegionOperation(ctx, op.Name)
}

// ============================================================================
// Routers
// ============================================================================

func (c *Client) InsertRouter(ctx context.Context, router *compute.Router) error {
	op, err := c.service.Routers.Insert(c.projectID, c.region, router).Context(ctx).Do()
	if err != nil {
		return wrapAuthError("inserting router", err)
	}
	return c.waitRegionOperation(ctx, op.Name)
}

func (c *Client) GetRouter(ctx context.Context, name string) (*compute.Router, error) {
	r, err := c.service.Routers.Get(c.projectID, c.region, name).Context(ctx).Do()
	if err != nil {
		return nil, wrapAuthError("getting router", err)
	}
	return r, nil
}

func (c *Client) PatchRouter(ctx context.Context, name string, router *compute.Router) error {
	op, err := c.service.Routers.Patch(c.projectID, c.region, name, router).Context(ctx).Do()
	if err != nil {
		return wrapAuthError("patching router", err)
	}
	return c.waitRegionOperation(ctx, op.Name)
}

func (c *Client) DeleteRouter(ctx context.Context, name string) error {
	op, err := c.service.Routers.Delete(c.projectID, c.region, name).Context(ctx).Do()
	if err != nil {
		return wrapAuthError("deleting router", err)
	}
	return c.waitRegionOperation(ctx, op.Name)
}

// ============================================================================
// Firewalls
// ============================================================================

func (c *Client) InsertFirewall(ctx context.Context, firewall *compute.Firewall) error {
	op, err := c.service.Firewalls.Insert(c.projectID, firewall).Context(ctx).Do()
	if err != nil {
		return wrapAuthError("inserting firewall", err)
	}
	return c.waitGlobalOperation(ctx, op.Name)
}

func (c *Client) GetFirewall(ctx context.Context, name string) (*compute.Firewall, error) {
	fw, err := c.service.Firewalls.Get(c.projectID, name).Context(ctx).Do()
	if err != nil {
		return nil, wrapAuthError("getting firewall", err)
	}
	return fw, nil
}

func (c *Client) DeleteFirewall(ctx context.Context, name string) error {
	op, err := c.service.Firewalls.Delete(c.projectID, name).Context(ctx).Do()
	if err != nil {
		return wrapAuthError("deleting firewall", err)
	}
	return c.waitGlobalOperation(ctx, op.Name)
}

// ============================================================================
// Operations
// ============================================================================

func (c *Client) waitGlobalOperation(ctx context.Context, opName string) error {
	waitCtx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	for {
		op, err := c.service.GlobalOperations.Wait(c.projectID, opName).Context(waitCtx).Do()
		if err != nil {
			return fmt.Errorf("waiting for operation: %w", err)
		}
		if op.Status == "DONE" {
			if op.Error != nil {
				return fmt.Errorf("operation failed: %s", formatOperationErrors(op.Error.Errors))
			}
			return nil
		}
		select {
		case <-time.After(operationPollInterval):
		case <-waitCtx.Done():
			return fmt.Errorf("operation canceled during polling: %w", waitCtx.Err())
		}
	}
}

func (c *Client) waitRegionOperation(ctx context.Context, opName string) error {
	waitCtx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	for {
		op, err := c.service.RegionOperations.Wait(c.projectID, c.region, opName).Context(waitCtx).Do()
		if err != nil {
			return fmt.Errorf("waiting for operation: %w", err)
		}
		if op.Status == "DONE" {
			if op.Error != nil {
				return fmt.Errorf("operation failed: %s", formatOperationErrors(op.Error.Errors))
			}
			return nil
		}
		select {
		case <-time.After(operationPollInterval):
		case <-waitCtx.Done():
			return fmt.Errorf("operation canceled during polling: %w", waitCtx.Err())
		}
	}
}

func formatOperationErrors(errors []*compute.OperationErrorErrors) string {
	if len(errors) == 0 {
		return "unknown error"
	}
	var messages []string
	for _, e := range errors {
		messages = append(messages, fmt.Sprintf("%s: %s", e.Code, e.Message))
	}
	return strings.Join(messages, "; ")
}
