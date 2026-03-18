// Package auditlog provides a client for querying Cloud Audit Logs.
package auditlog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	logging "google.golang.org/api/logging/v2"
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
			"  Ensure your account has the required role:\n"+
			"    - roles/logging.viewer\n\n"+
			"  Check: gcloud projects get-iam-policy <project> --flatten='bindings[].members' --filter='bindings.members:<your-email>'", action)
	case strings.Contains(msg, "Unauthenticated") || strings.Contains(msg, "401"):
		return fmt.Errorf("%s: authentication failed\n\n"+
			"  Run: gcloud auth application-default login\n"+
			"  Or: gcloud auth login", action)
	default:
		return fmt.Errorf("%s: %w", action, err)
	}
}

// Client wraps the Cloud Logging API for audit log queries.
type Client struct {
	project string
	svc     *logging.Service
}

// NewClient creates a new audit log client for the given project.
func NewClient(ctx context.Context, project string) (*Client, error) {
	svc, err := logging.NewService(ctx)
	if err != nil {
		return nil, wrapAuthError("creating logging client", err)
	}
	return &Client{project: project, svc: svc}, nil
}

// QueryOptions configures the audit log query.
type QueryOptions struct {
	Workflow  string
	Region    string
	Freshness time.Duration
	Limit     int
}

// AuditEntry represents a single audit log entry for a workflow execution.
type AuditEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	User        string    `json:"user"`
	Workflow    string    `json:"workflow"`
	ExecutionID string    `json:"execution_id,omitempty"`
}

// QueryWorkflowAuditLogs queries Cloud Audit Logs for workflow execution events.
func (c *Client) QueryWorkflowAuditLogs(ctx context.Context, opts QueryOptions) ([]AuditEntry, error) {
	since := time.Now().UTC().Add(-opts.Freshness).Format(time.RFC3339)

	filter := fmt.Sprintf(
		`resource.type="audited_resource"
protoPayload.serviceName="workflowexecutions.googleapis.com"
protoPayload.methodName="google.cloud.workflows.executions.v1.Executions.CreateExecution"
timestamp >= "%s"`, since)

	if opts.Workflow != "" {
		filter += fmt.Sprintf("\nprotoPayload.resourceName:\"/workflows/%s\"", opts.Workflow)
	}

	req := &logging.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + c.project},
		Filter:        filter,
		OrderBy:       "timestamp desc",
		PageSize:      int64(opts.Limit),
	}

	var entries []AuditEntry

	call := c.svc.Entries.List(req)
	err := call.Pages(ctx, func(resp *logging.ListLogEntriesResponse) error {
		for _, entry := range resp.Entries {
			ae := parseAuditEntry(entry)
			entries = append(entries, ae)
			if len(entries) >= opts.Limit {
				return fmt.Errorf("limit reached")
			}
		}
		return nil
	})
	// "limit reached" is our sentinel, not a real error.
	if err != nil && err.Error() != "limit reached" {
		return nil, wrapAuthError("querying audit logs", err)
	}

	return entries, nil
}

func parseAuditEntry(entry *logging.LogEntry) AuditEntry {
	ae := AuditEntry{}

	if t, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
		ae.Timestamp = t
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(entry.ProtoPayload, &payload); err != nil {
		return ae
	}

	if authInfo, ok := payload["authenticationInfo"].(map[string]interface{}); ok {
		if email, ok := authInfo["principalEmail"].(string); ok {
			ae.User = email
		}
	}

	if resourceName, ok := payload["resourceName"].(string); ok {
		if idx := strings.Index(resourceName, "/workflows/"); idx != -1 {
			rest := resourceName[idx+len("/workflows/"):]
			if execIdx := strings.Index(rest, "/executions/"); execIdx != -1 {
				ae.Workflow = rest[:execIdx]
				ae.ExecutionID = rest[execIdx+len("/executions/"):]
			} else {
				ae.Workflow = rest
			}
		}
	}

	if ae.ExecutionID == "" {
		if response, ok := payload["response"].(map[string]interface{}); ok {
			if name, ok := response["name"].(string); ok {
				if idx := strings.Index(name, "/executions/"); idx != -1 {
					ae.ExecutionID = name[idx+len("/executions/"):]
				}
			}
		}
	}

	return ae
}
