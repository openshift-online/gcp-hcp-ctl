package cli

import (
	"fmt"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show effective configuration",
		Long: `Show the effective CLI configuration after merging config file,
environment variables, and CLI flags.

Config file location: ~/.gcphcpctl/config.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath := configPath
			if cfgPath == "" {
				cfgPath = config.DefaultConfigPath()
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Config file:    %s\n", valueOrNone(cfgPath))
			fmt.Fprintf(w, "Project:        %s\n", valueOrNone(project))
			fmt.Fprintf(w, "Region:         %s\n", valueOrNone(region))
			fmt.Fprintf(w, "Output:         %s\n", outputFormat)
			fmt.Fprintf(w, "API endpoint:   %s\n", valueOrNone(apiEndpoint))
			fmt.Fprintf(w, "OIDC endpoint:  %s\n", valueOrNone(oidcEndpoint))
			return nil
		},
	}
	return cmd
}

func valueOrNone(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}
