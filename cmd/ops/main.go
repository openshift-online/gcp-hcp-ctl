// Standalone entry point for the ops plugin (gcphcpctl-ops).
// This can be built and distributed independently of the main gcphcpctl binary.
//
// Build with: make build-ops
package main

import (
	"fmt"
	"os"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/config"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/ops"

	"github.com/spf13/cobra"
)

var (
	project      string
	region       string
	outputFormat string
	configPath   string
)

func main() {
	root := ops.NewOpsCmd()
	root.Use = "gcphcpctl-ops"
	root.Short = "Operational commands for GCP HCP cluster debugging"
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}
		if project == "" && cfg.Project != "" {
			project = cfg.Project
		}
		if region == "" && cfg.Region != "" {
			region = cfg.Region
		}
		if !cmd.Flags().Changed("output") && cfg.Output != "" {
			outputFormat = cfg.Output
		}
		return ops.ValidateRequiredFlags(cmd)
	}

	root.PersistentFlags().StringVar(&project, "project", os.Getenv("GCPHCPCTL_PROJECT"), "GCP project ID (env: GCPHCPCTL_PROJECT)")
	root.PersistentFlags().StringVar(&region, "region", os.Getenv("GCPHCPCTL_REGION"), "GCP region (env: GCPHCPCTL_REGION)")
	root.PersistentFlags().StringVarP(&outputFormat, "output", "o", "text", "Output format: text, json, yaml")
	root.PersistentFlags().StringVar(&configPath, "config", "", "Config file path (default: ~/.gcphcpctl/config.yaml)")

	root.SilenceUsage = true
	root.SilenceErrors = true

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
