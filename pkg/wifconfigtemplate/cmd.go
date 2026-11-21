// Package wifconfigtemplate exposes read-only views of the platform IAM catalog.
package wifconfigtemplate

import (
	"fmt"
	"sort"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/internal/access"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

// NewWIFConfigTemplateCmd returns authenticated, read-only catalog commands.
func NewWIFConfigTemplateCmd() *cobra.Command {
	group := access.Group("wifconfigtemplate", "View WIF configuration templates")
	group.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List WIF configuration templates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := access.Client(cmd).WIFConfigTemplates().List(cmd.Context())
			if err != nil {
				return fmt.Errorf("listing WIF configuration templates: %w", err)
			}
			list = list.DeepCopy()
			prepareList(list)
			sort.Slice(list.Items, func(i, j int) bool {
				left, leftErr := utilversion.ParseSemantic(list.Items[i].Name)
				right, rightErr := utilversion.ParseSemantic(list.Items[j].Name)
				if leftErr != nil || rightErr != nil {
					if leftErr == nil {
						return true
					}
					if rightErr == nil {
						return false
					}
					return list.Items[i].Name < list.Items[j].Name
				}
				return left.LessThan(right)
			})
			switch access.Format(cmd) {
			case output.FormatJSON:
				return output.PrintJSON(cmd.OutOrStdout(), list)
			case output.FormatYAML:
				return output.PrintResourceYAML(cmd.OutOrStdout(), list)
			}
			if len(list.Items) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No WIF configuration templates found.")
				return err
			}
			table := output.NewTable(cmd.OutOrStdout(), "NAME", "VERIFIED", "CONTRACT DIGEST")
			for _, item := range list.Items {
				digest, err := application.Digest(item.Spec)
				if err != nil {
					return fmt.Errorf("digest for template %q: %w", item.Name, err)
				}
				table.AddRow(item.Name, fmt.Sprint(item.Spec.Verified != nil && *item.Spec.Verified), digest)
			}
			return table.Flush()
		},
	})
	group.AddCommand(&cobra.Command{
		Use:   "get <name>",
		Short: "Get a WIF configuration template",
		Args:  access.Named,
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := access.Client(cmd).WIFConfigTemplates().Get(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("getting WIF configuration template %q: %w", args[0], err)
			}
			item = item.DeepCopy()
			prepareItem(item)
			switch access.Format(cmd) {
			case output.FormatJSON:
				return output.PrintJSON(cmd.OutOrStdout(), item)
			case output.FormatYAML:
				return output.PrintResourceYAML(cmd.OutOrStdout(), item)
			}
			digest, err := application.Digest(item.Spec)
			if err != nil {
				return fmt.Errorf("digest for template %q: %w", item.Name, err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Name:            %s\nUID:             %s\nVerified:        %t\nSchema Version:  %d\nContract Digest: %s\nUse -o yaml to view the full IAM contract.\n",
				item.Name, item.UID, item.Spec.Verified != nil && *item.Spec.Verified, item.Spec.SchemaVersion, digest)
			return err
		},
	})
	return group
}

func prepareItem(item *gcpv1.WIFConfigTemplate) {
	item.ManagedFields = nil
	if item.APIVersion == "" {
		item.APIVersion = gcpv1.GroupVersion.String()
	}
	if item.Kind == "" {
		item.Kind = "WIFConfigTemplate"
	}
}

func prepareList(list *gcpv1.WIFConfigTemplateList) {
	if list.Items == nil {
		list.Items = []gcpv1.WIFConfigTemplate{}
	}
	if list.APIVersion == "" {
		list.APIVersion = gcpv1.GroupVersion.String()
	}
	if list.Kind == "" {
		list.Kind = "WIFConfigTemplateList"
	}
	for i := range list.Items {
		prepareItem(&list.Items[i])
	}
}
