package rolebinding

import (
	"fmt"
	"strings"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/internal/access"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newCreateCmd() *cobra.Command {
	var subject, platformRole, role string
	var conditions []string
	var parsed []gcpv1.RoleBindingRequirement
	cmd := &cobra.Command{Use: "create <name>", Short: "Grant one user email one PlatformRole or custom Role", Long: `Create a RoleBinding granting a user a PlatformRole or custom Role in the
selected project. Optionally supply a condition to restrict this binding's
grant. Existing bindings are never overwritten.`, Example: createExamples, Args: conditionArgs(&conditions, &parsed, false), RunE: func(cmd *cobra.Command, args []string) error {
		email, err := NormalizeEmail(subject)
		if err != nil {
			return fmt.Errorf("invalid --subject: %w", err)
		}
		p, r := cmd.Flags().Changed("platform-role"), cmd.Flags().Changed("role")
		if p == r {
			return fmt.Errorf("exactly one of --platform-role or --role is required")
		}
		kind, name := "Role", role
		if p {
			kind, name = "PlatformRole", platformRole
		}
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("role reference must not be empty")
		}
		c := access.Client(cmd)
		binding, err := c.RoleBindings().Create(cmd.Context(), c.Namespace(), &gcpv1.RoleBinding{
			TypeMeta: metav1.TypeMeta{APIVersion: gcpv1.GroupVersion.String(), Kind: "RoleBinding"}, ObjectMeta: metav1.ObjectMeta{Name: args[0], Namespace: c.Namespace()},
			Spec: gcpv1.RoleBindingSpec{Subject: email, RoleRef: gcpv1.RoleRef{Kind: kind, Name: name, APIGroup: gcpv1.GroupVersion.Group}, Conditions: parsed}})
		if err != nil {
			return err
		}
		if err = printBinding(cmd, binding); err != nil {
			return err
		}
		return access.Note(cmd)
	}}
	cmd.Flags().StringArrayVar(&conditions, "condition", nil, "Requirement (repeatable; see Conditions below)")
	cmd.Flags().StringVar(&subject, "subject", "", "User email principal (required)")
	cmd.Flags().StringVar(&platformRole, "platform-role", "", "PlatformRole name (exclusive with --role)")
	cmd.Flags().StringVar(&role, "role", "", "Custom Role name in the selected project (exclusive with --platform-role)")
	cmd.MarkFlagsMutuallyExclusive("platform-role", "role")
	setConditionHelp(cmd)
	return cmd
}

const createExamples = `  gcphcpctl rolebinding create regional-viewer \
    --subject alice@example.com --platform-role cluster-viewer \
    --condition 'region In ["us-east1", "europe-*"]' \
    --condition 'name In ["demo-*"]'`
