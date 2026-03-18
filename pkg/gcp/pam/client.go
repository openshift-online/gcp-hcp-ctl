// Package pam provides a client for managing GCP Privileged Access Manager grants.
package pam

import (
	"context"
	"fmt"
	"strings"
	"time"

	pam "cloud.google.com/go/privilegedaccessmanager/apiv1"
	pb "cloud.google.com/go/privilegedaccessmanager/apiv1/privilegedaccessmanagerpb"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/durationpb"
)

func wrapAuthError(action string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "could not find default credentials"):
		return fmt.Errorf("%s: no GCP credentials found\n\n"+
			"  Run: gcloud auth application-default login\n"+
			"  Or set GOOGLE_APPLICATION_CREDENTIALS to a service account key file", action)
	case strings.Contains(msg, "token expired") || strings.Contains(msg, "oauth2: token expired"):
		return fmt.Errorf("%s: GCP credentials have expired\n\n"+
			"  Run: gcloud auth application-default login", action)
	case strings.Contains(msg, "PermissionDenied") || strings.Contains(msg, "permission denied") || strings.Contains(msg, "403"):
		return fmt.Errorf("%s: permission denied\n\n"+
			"  %v\n\n"+
			"  Ensure your account is an eligible requester or approver for the PAM entitlement.\n"+
			"  Check with your administrator that your group is listed in the entitlement configuration.\n"+
			"  Also verify the PAM API is enabled: gcloud services enable privilegedaccessmanager.googleapis.com --project <project>", action, err)
	case strings.Contains(msg, "NotFound") || strings.Contains(msg, "not found"):
		return fmt.Errorf("%s: resource not found\n\n"+
			"  Verify the entitlement or grant ID exists:\n"+
			"    gcphcp ops pam list --project <project> --region <region>", action)
	case strings.Contains(msg, "Unauthenticated") || strings.Contains(msg, "401"):
		return fmt.Errorf("%s: authentication failed\n\n"+
			"  Run: gcloud auth application-default login", action)
	default:
		return fmt.Errorf("%s: %w", action, err)
	}
}

// Client wraps the GCP Privileged Access Manager API.
type Client struct {
	Project string
	client  *pam.Client
}

// EntitlementInfo holds metadata about a PAM entitlement.
type EntitlementInfo struct {
	Name             string        `json:"name"`
	State            string        `json:"state"`
	MaxDuration      time.Duration `json:"max_duration"`
	RequiresApproval bool          `json:"requires_approval"`
}

// GrantInfo holds metadata about a PAM grant.
type GrantInfo struct {
	Name               string        `json:"name"`
	State              string        `json:"state"`
	Requester          string        `json:"requester"`
	Entitlement        string        `json:"entitlement"`
	RequestedDuration  time.Duration `json:"requested_duration"`
	CreateTime         time.Time     `json:"create_time"`
	ActivateTime       time.Time     `json:"activate_time,omitempty"`
	ApprovalExpireTime time.Time     `json:"approval_expire_time,omitempty"`
}

// ShortName returns the last segment of the grant's full resource name.
func (g *GrantInfo) ShortName() string {
	parts := strings.Split(g.Name, "/")
	return parts[len(parts)-1]
}

// ShortEntitlement returns the last segment of the entitlement's full resource name.
func (g *GrantInfo) ShortEntitlement() string {
	return ShortEntitlementName(g.Entitlement)
}

// ShortEntitlementName extracts the entitlement ID from a full resource name.
func ShortEntitlementName(fullName string) string {
	parts := strings.Split(fullName, "/")
	for i, p := range parts {
		if p == "entitlements" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return fullName
}

// NewClient creates a new PAM client using Application Default Credentials.
func NewClient(ctx context.Context, project string) (*Client, error) {
	c, err := pam.NewClient(ctx)
	if err != nil {
		return nil, wrapAuthError("creating PAM client", err)
	}
	return &Client{
		Project: project,
		client:  c,
	}, nil
}

// Close releases resources held by the client.
func (c *Client) Close() error {
	return c.client.Close()
}

func (c *Client) entitlementParent() string {
	return fmt.Sprintf("projects/%s/locations/global", c.Project)
}

// SearchEntitlements returns entitlements the caller can request grants for.
func (c *Client) SearchEntitlements(ctx context.Context) ([]EntitlementInfo, error) {
	var result []EntitlementInfo

	it := c.client.SearchEntitlements(ctx, &pb.SearchEntitlementsRequest{
		Parent:           c.entitlementParent(),
		CallerAccessType: pb.SearchEntitlementsRequest_GRANT_REQUESTER,
	})

	for {
		ent, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, wrapAuthError("searching entitlements", err)
		}

		info := EntitlementInfo{
			Name:  ent.Name,
			State: ent.State.String(),
		}
		if ent.MaxRequestDuration != nil {
			info.MaxDuration = ent.MaxRequestDuration.AsDuration()
		}
		if ent.ApprovalWorkflow != nil {
			info.RequiresApproval = true
		}

		result = append(result, info)
	}

	return result, nil
}

// CreateGrant requests a new PAM grant against an entitlement.
func (c *Client) CreateGrant(ctx context.Context, entitlementName string, duration time.Duration, justification string) (*GrantInfo, error) {
	grant := &pb.Grant{
		RequestedDuration: durationpb.New(duration),
		Justification: &pb.Justification{
			Justification: &pb.Justification_UnstructuredJustification{
				UnstructuredJustification: justification,
			},
		},
	}

	resp, err := c.client.CreateGrant(ctx, &pb.CreateGrantRequest{
		Parent: entitlementName,
		Grant:  grant,
	})
	if err != nil {
		return nil, wrapAuthError("creating grant", err)
	}

	return grantInfoFromProto(resp), nil
}

// GetGrant retrieves the current state of a grant.
func (c *Client) GetGrant(ctx context.Context, grantName string) (*GrantInfo, error) {
	resp, err := c.client.GetGrant(ctx, &pb.GetGrantRequest{
		Name: grantName,
	})
	if err != nil {
		return nil, wrapAuthError("getting grant", err)
	}
	return grantInfoFromProto(resp), nil
}

// ApproveGrant approves a pending grant (for approvers).
func (c *Client) ApproveGrant(ctx context.Context, grantName string, reason string) (*GrantInfo, error) {
	resp, err := c.client.ApproveGrant(ctx, &pb.ApproveGrantRequest{
		Name:   grantName,
		Reason: reason,
	})
	if err != nil {
		return nil, wrapAuthError("approving grant", err)
	}
	return grantInfoFromProto(resp), nil
}

// RevokeGrant immediately revokes an active grant.
func (c *Client) RevokeGrant(ctx context.Context, grantName string, reason string) (*GrantInfo, error) {
	op, err := c.client.RevokeGrant(ctx, &pb.RevokeGrantRequest{
		Name:   grantName,
		Reason: reason,
	})
	if err != nil {
		return nil, wrapAuthError("revoking grant", err)
	}

	grant, err := op.Wait(ctx)
	if err != nil {
		return nil, wrapAuthError("waiting for grant revocation", err)
	}
	return grantInfoFromProto(grant), nil
}

// DenyGrant denies a pending grant (for approvers).
func (c *Client) DenyGrant(ctx context.Context, grantName string, reason string) (*GrantInfo, error) {
	resp, err := c.client.DenyGrant(ctx, &pb.DenyGrantRequest{
		Name:   grantName,
		Reason: reason,
	})
	if err != nil {
		return nil, wrapAuthError("denying grant", err)
	}
	return grantInfoFromProto(resp), nil
}

// CallerRelationship specifies the relationship between the caller and grants to search.
type CallerRelationship int

const (
	// RelationshipCreated returns grants created by the caller.
	RelationshipCreated CallerRelationship = iota
	// RelationshipApproved returns grants the caller has approved.
	RelationshipApproved
	// RelationshipCanApprove returns grants pending the caller's approval.
	RelationshipCanApprove
)

// SearchGrants returns grants for the given entitlement, filtered by caller relationship.
func (c *Client) SearchGrants(ctx context.Context, entitlementName string, rel CallerRelationship) ([]GrantInfo, error) {
	var pbRel pb.SearchGrantsRequest_CallerRelationshipType
	switch rel {
	case RelationshipCreated:
		pbRel = pb.SearchGrantsRequest_HAD_CREATED
	case RelationshipApproved:
		pbRel = pb.SearchGrantsRequest_HAD_APPROVED
	case RelationshipCanApprove:
		pbRel = pb.SearchGrantsRequest_CAN_APPROVE
	}

	var result []GrantInfo

	it := c.client.SearchGrants(ctx, &pb.SearchGrantsRequest{
		Parent:             entitlementName,
		CallerRelationship: pbRel,
	})

	for {
		grant, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, wrapAuthError("searching grants", err)
		}
		result = append(result, *grantInfoFromProto(grant))
	}

	return result, nil
}

// ListGrants returns all grants for the given entitlement, optionally filtered.
func (c *Client) ListGrants(ctx context.Context, entitlementName string, filter string) ([]GrantInfo, error) {
	var result []GrantInfo

	it := c.client.ListGrants(ctx, &pb.ListGrantsRequest{
		Parent: entitlementName,
		Filter: filter,
	})

	for {
		grant, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, wrapAuthError("listing grants", err)
		}
		result = append(result, *grantInfoFromProto(grant))
	}

	return result, nil
}

// WaitForGrant polls until the grant state leaves APPROVAL_AWAITED.
func (c *Client) WaitForGrant(ctx context.Context, grantName string) (*GrantInfo, error) {
	pollInterval := 2 * time.Second
	maxPoll := 10 * time.Second

	for {
		info, err := c.GetGrant(ctx, grantName)
		if err != nil {
			return nil, err
		}

		if info.State != "APPROVAL_AWAITED" {
			return info, nil
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for grant approval\n\n"+
				"  Check status with: gcphcp ops pam status %s", grantName)
		case <-time.After(pollInterval):
		}

		if pollInterval < maxPoll {
			pollInterval = pollInterval * 2
			if pollInterval > maxPoll {
				pollInterval = maxPoll
			}
		}
	}
}

func grantInfoFromProto(g *pb.Grant) *GrantInfo {
	info := &GrantInfo{
		Name:      g.Name,
		State:     g.State.String(),
		Requester: g.Requester,
	}

	// Extract entitlement from grant name: .../entitlements/<id>/grants/<gid>
	parts := strings.Split(g.Name, "/grants/")
	if len(parts) > 0 {
		info.Entitlement = parts[0]
	}

	if g.RequestedDuration != nil {
		info.RequestedDuration = g.RequestedDuration.AsDuration()
	}

	if g.CreateTime != nil {
		info.CreateTime = g.CreateTime.AsTime()
	}
	if g.Timeline != nil {
		for _, event := range g.Timeline.Events {
			switch ev := event.GetEvent().(type) {
			case *pb.Grant_Timeline_Event_Activated_:
				_ = ev
				info.ActivateTime = event.EventTime.AsTime()
			case *pb.Grant_Timeline_Event_Requested_:
				if ev.Requested != nil && ev.Requested.ExpireTime != nil {
					info.ApprovalExpireTime = ev.Requested.ExpireTime.AsTime()
				}
			}
		}
	}

	return info
}

// RemainingTime returns the time remaining for the grant based on its state.
// For APPROVAL_AWAITED: time until approval expires.
// For ACTIVE: time until access ends.
// Returns zero if not applicable.
func (g *GrantInfo) RemainingTime() time.Duration {
	now := time.Now()
	switch g.State {
	case "APPROVAL_AWAITED":
		if !g.ApprovalExpireTime.IsZero() && g.ApprovalExpireTime.After(now) {
			return g.ApprovalExpireTime.Sub(now).Truncate(time.Second)
		}
	case "ACTIVE", "ACTIVATED":
		if !g.ActivateTime.IsZero() && g.RequestedDuration > 0 {
			endTime := g.ActivateTime.Add(g.RequestedDuration)
			if endTime.After(now) {
				return endTime.Sub(now).Truncate(time.Second)
			}
		}
	}
	return 0
}
