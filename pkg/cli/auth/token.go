// Package auth contains internal auth subcommands for gcphcpctl.
// The commands in this package are hidden from the main help text; they exist
// to be used as kubectl exec credential plugins written into kubeconfig files
// by "gcphcpctl cluster login".
package auth

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/auth"
	"github.com/spf13/cobra"
)

// execCredential is the ExecCredential response shape expected by kubectl.
// It follows the client.authentication.k8s.io/v1beta1 schema.
type execCredential struct {
	APIVersion string               `json:"apiVersion"`
	Kind       string               `json:"kind"`
	Status     execCredentialStatus `json:"status"`
}

// execCredentialStatus carries the token and its expiry for kubectl's exec plugin protocol.
// Populating expirationTimestamp lets kubectl pro-actively refresh before expiry
// rather than waiting for a 401.
type execCredentialStatus struct {
	Token               string     `json:"token"`
	ExpirationTimestamp *time.Time `json:"expirationTimestamp,omitempty"`
}

// NewAuthCmd returns the hidden "auth" parent command.
func NewAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "auth",
		Short:  "Authentication helpers (internal use)",
		Hidden: true,
	}
	cmd.AddCommand(newTokenCmd())
	return cmd
}

func newTokenCmd() *cobra.Command {
	var audience string

	cmd := &cobra.Command{
		Use:    "token",
		Short:  "Print a kubectl ExecCredential JSON with a Google identity token",
		Hidden: true,
		Long: `Fetches a Google identity token and writes a client.authentication.k8s.io/v1beta1
ExecCredential JSON object to stdout. This command is intended to be used as a
kubectl exec credential plugin — it is written into kubeconfig files by
"gcphcpctl cluster login" and called automatically by kubectl.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if audience == "" {
				return fmt.Errorf("--audience is required")
			}

			ts := auth.NewTokenSource(audience)
			token, _, expiry, err := ts.TokenWithExpiry(cmd.Context())
			if err != nil {
				return fmt.Errorf("fetching identity token: %w", err)
			}

			status := execCredentialStatus{Token: token}
			if !expiry.IsZero() {
				status.ExpirationTimestamp = &expiry
			}
			cred := execCredential{
				APIVersion: "client.authentication.k8s.io/v1beta1",
				Kind:       "ExecCredential",
				Status:     status,
			}
			out, err := json.Marshal(cred)
			if err != nil {
				return fmt.Errorf("marshaling ExecCredential: %w", err)
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(out)); err != nil {
				return fmt.Errorf("writing ExecCredential to stdout: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&audience, "audience", "", "Identity token audience (the cluster API server URL)")
	return cmd
}
