// Standalone entry point for the ops plugin (gcphcp-ops).
// This can be built and distributed independently of the main gcphcp binary.
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
	root.Use = "gcphcp-ops"
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
		return nil
	}

	root.PersistentFlags().StringVar(&project, "project", os.Getenv("GCPHCP_PROJECT"), "GCP project ID (env: GCPHCP_PROJECT)")
	root.PersistentFlags().StringVar(&region, "region", os.Getenv("GCPHCP_REGION"), "GCP region (env: GCPHCP_REGION)")
	root.PersistentFlags().StringVarP(&outputFormat, "output", "o", "text", "Output format: text, json, yaml")
	root.PersistentFlags().StringVar(&configPath, "config", "", "Config file path (default: ~/.gcphcp/config.yaml)")

	root.SilenceUsage = true
	root.SilenceErrors = true

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
