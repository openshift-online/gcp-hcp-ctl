package ops

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/gcp/workflows"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/output"
	"github.com/spf13/cobra"
)

// resourceTypeExpand expands short aliases to full resource types.
var resourceTypeExpand = map[string]string{
	"hc":     "hostedclusters",
	"np":     "nodepools",
	"hcp":    "hostedcontrolplanes",
	"deploy": "deployments",
	"sts":    "statefulsets",
	"rs":     "replicasets",
	"ds":     "daemonsets",
	"svc":    "services",
	"cm":     "configmaps",
	"ep":     "endpoints",
	"ns":     "namespaces",
	"pvc":    "persistentvolumeclaims",
	"pv":     "persistentvolumes",
	"sa":     "serviceaccounts",
	"po":     "pods",
	"ev":     "events",
	"no":     "nodes",

	"pod":                   "pods",
	"deployment":            "deployments",
	"statefulset":           "statefulsets",
	"replicaset":            "replicasets",
	"daemonset":             "daemonsets",
	"service":               "services",
	"configmap":             "configmaps",
	"endpoint":              "endpoints",
	"namespace":             "namespaces",
	"node":                  "nodes",
	"event":                 "events",
	"serviceaccount":        "serviceaccounts",
	"hostedcluster":         "hostedclusters",
	"nodepool":              "nodepools",
	"hostedcontrolplane":    "hostedcontrolplanes",
	"persistentvolumeclaim": "persistentvolumeclaims",
	"persistentvolume":      "persistentvolumes",
}

func newGetCmd() *cobra.Command {
	var (
		namespace     string
		labelSelector string
		analyze       bool
		timeout       time.Duration
	)

	cmd := &cobra.Command{
		Use:   "get <resource-type> [resource-name]",
		Short: "Get Kubernetes resources via Cloud Workflows",
		Long: `Get Kubernetes resources from a GKE cluster using the get workflow.
Works like kubectl get but runs through Cloud Workflows.

Examples:
  # List all pods in a namespace
  gcphcpctl ops get pods -n hypershift

  # Get a specific pod
  gcphcpctl ops get pods my-pod -n hypershift

  # AI-powered pod analysis
  gcphcpctl ops get pods my-pod -n hypershift --analyze

  # List all hosted clusters
  gcphcpctl ops get hostedclusters -n clusters

  # Short aliases work too
  gcphcpctl ops get hc -n clusters
  gcphcpctl ops get deploy -n clusters-test-pd-test-pd

  # Filter by label selector
  gcphcpctl ops get pods -n hypershift -l app=nginx

  # List cluster-scoped resources
  gcphcpctl ops get nodes
  gcphcpctl ops get namespaces`,

		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resourceType := args[0]
			if expanded, ok := resourceTypeExpand[resourceType]; ok {
				resourceType = expanded
			}

			var resourceName string
			if len(args) > 1 {
				resourceName = args[1]
			}

			if analyze && (resourceType != "pods" || resourceName == "") {
				return fmt.Errorf("--analyze requires a specific pod name (e.g. gcphcpctl ops get pods my-pod -n ns --analyze)")
			}

			project, _ := cmd.Flags().GetString("project")
			region, _ := cmd.Flags().GetString("region")
			outputFormat, _ := cmd.Flags().GetString("output")


			data := map[string]interface{}{
				"resource_type": resourceType,
			}
			if namespace != "" {
				data["namespace"] = namespace
			}
			if resourceName != "" {
				data["name"] = resourceName
			}
			if labelSelector != "" {
				data["label_selector"] = labelSelector
			}
			if analyze {
				data["analyze"] = true
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			client, err := workflows.NewClient(ctx, project, region)
			if err != nil {
				return fmt.Errorf("creating client: %w", err)
			}
			defer client.Close()

			if err := checkPAMGate(ctx, client, "get", cmd, os.Stderr); err != nil {
				return err
			}

			if analyze {
				fmt.Fprintf(os.Stderr, "Analyzing %s/%s in %s (this may take a moment)...\n", resourceType, resourceName, namespace)
			} else {
				fmt.Fprintf(os.Stderr, "Getting %s", resourceType)
				if resourceName != "" {
					fmt.Fprintf(os.Stderr, " %s", resourceName)
				}
				if namespace != "" {
					fmt.Fprintf(os.Stderr, " (ns: %s)", namespace)
				}
				if labelSelector != "" {
					fmt.Fprintf(os.Stderr, " (selector: %s)", labelSelector)
				}
				fmt.Fprintln(os.Stderr)
			}

			_, result, err := client.Run(ctx, "get", data)
			if err != nil {
				return fmt.Errorf("executing workflow: %w", err)
			}

			if result.State == "FAILED" {
				return fmt.Errorf("workflow failed: %s", result.Error)
			}

			format := output.ParseFormat(outputFormat)
			if format == output.FormatJSON {
				return output.PrintJSON(os.Stdout, result.Result)
			}

			if analyze {
				return output.PrintAnalysis(os.Stdout, result.Result, namespace)
			}

			return output.PrintResourceTable(os.Stdout, result.Result, resourceType)
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Kubernetes namespace")
	cmd.Flags().StringVarP(&labelSelector, "selector", "l", "", "Label selector (e.g. app=nginx)")
	cmd.Flags().BoolVar(&analyze, "analyze", false, "Run AI analysis on a pod (requires a specific pod name)")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Maximum time to wait for workflow completion")

	return cmd
}
