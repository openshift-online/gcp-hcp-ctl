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
	var subject, role, platformRole, condition string
	cmd := &cobra.Command{
		Use: "update <name>", Short: "Update supplied fields of a RoleBinding",
		Long: `Update the subject, role reference, or condition of an existing RoleBinding.
Only supplied fields change; omitted fields and binding identity are
preserved. At least one update flag is required. Unlike role update's
permission-set replacement, this is a partial update.`,
		Example: updateExamples,
		Args:    access.Named,
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
				patch.Condition = &condition
			}
			if patch.Subject == nil && patch.RoleRef == nil && patch.Condition == nil {
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
	cmd.Flags().StringVar(&condition, "condition", "", "Cedar expression (see Conditions below)")
	cmd.MarkFlagsMutuallyExclusive("role", "platform-role")
	setConditionHelp(cmd)
	return cmd
}

const updateExamples = `  # Change the subject, preserving the role reference and condition.
  gcphcpctl rolebinding update viewer --subject bob@example.com

  # Switch to a custom Role or a PlatformRole.
  gcphcpctl rolebinding update viewer --role limited-operations
  gcphcpctl rolebinding update viewer --platform-role cluster-viewer

  # Replace the condition.
  gcphcpctl rolebinding update viewer \
    --condition 'context.resourcePlural == "clusters" && context.resourceName like "bob*"'

  # Clear the condition.
  gcphcpctl rolebinding update viewer --condition ''

  # Change several fields together.
  gcphcpctl rolebinding update viewer \
    --subject bob@example.com --role limited-operations --condition ''`
