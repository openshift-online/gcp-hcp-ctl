package rolebinding

import (
	"fmt"
	"strings"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/internal/access"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var subject, role, platformRole string
	var conditions []string
	var parsed []gcpv1.RoleBindingRequirement
	var clear bool
	cmd := &cobra.Command{
		Use: "update <name>", Short: "Update supplied fields of a RoleBinding",
		Long: `Update the subject, role reference, or condition of an existing RoleBinding.
Only supplied fields change; omitted fields and binding identity are
preserved. At least one update flag is required. Unlike role update's
permission-set replacement, this is a partial update.`,
		Example: updateExamples,
		Args:    conditionArgs(&conditions, &parsed, true),
		RunE: func(cmd *cobra.Command, args []string) error {
			var patch platformapi.RoleBindingPatch
			if cmd.Flags().Changed("subject") {
				email, err := NormalizeEmail(subject)
				if err != nil {
					return fmt.Errorf("invalid --subject: %w", err)
				}
				patch.Subject = &email
			}
			p, r := cmd.Flags().Changed("platform-role"), cmd.Flags().Changed("role")
			if p && r {
				return fmt.Errorf("--platform-role and --role are mutually exclusive")
			}
			if p || r {
				kind, name := "Role", role
				if p {
					kind, name = "PlatformRole", platformRole
				}
				if strings.TrimSpace(name) == "" {
					return fmt.Errorf("role reference must not be empty")
				}
				patch.RoleRef = &gcpv1.RoleRef{Kind: kind, Name: name, APIGroup: gcpv1.GroupVersion.Group}
			}
			if cmd.Flags().Changed("condition") {
				patch.Conditions = &parsed
			}
			if clear {
				empty := make([]gcpv1.RoleBindingRequirement, 0)
				patch.Conditions = &empty
			}
			if patch.Subject == nil && patch.RoleRef == nil && patch.Conditions == nil {
				return fmt.Errorf("at least one update flag is required")
			}
			c := access.Client(cmd)
			binding, err := c.RoleBindings().Patch(cmd.Context(), c.Namespace(), args[0], patch)
			if err != nil {
				return err
			}
			if err := printBinding(cmd, binding); err != nil {
				return err
			}
			return access.Note(cmd)
		},
	}
	cmd.Flags().StringVar(&subject, "subject", "", "New user email principal")
	cmd.Flags().StringVar(&role, "role", "", "New custom Role name (exclusive with --platform-role)")
	cmd.Flags().StringVar(&platformRole, "platform-role", "", "New PlatformRole name (exclusive with --role)")
	cmd.Flags().StringArrayVar(&conditions, "condition", nil, "Replace complete requirement list (repeatable)")
	cmd.Flags().BoolVar(&clear, "clear-conditions", false, "Clear conditions, broadening this grant")
	cmd.MarkFlagsMutuallyExclusive("condition", "clear-conditions")
	cmd.MarkFlagsMutuallyExclusive("role", "platform-role")
	setConditionHelp(cmd)
	return cmd
}

const updateExamples = `  # Preserve conditions.
  gcphcpctl rolebinding update regional-viewer --subject bob@example.com

  # Replace the complete list.
  gcphcpctl rolebinding update regional-viewer --condition 'region NotIn ["us-central1"]'

  # Clear restrictions, broadening this grant.
  gcphcpctl rolebinding update regional-viewer --clear-conditions`
