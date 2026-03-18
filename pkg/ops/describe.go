package ops

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ckandag/gcp-hcp-cli/pkg/gcp/workflows"
	"github.com/ckandag/gcp-hcp-cli/pkg/output"
	"github.com/spf13/cobra"
)

func newDescribeCmd() *cobra.Command {
	var (
		namespace string
		timeout   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "describe <resource-type> <resource-name>",
		Short: "Describe a Kubernetes resource with events",
		Long: `Describe a Kubernetes resource with detailed info and related events.
Works like kubectl describe but runs through Cloud Workflows.

Examples:
  # Describe a pod
  gcphcp ops describe pods my-pod -n hypershift

  # Describe a deployment
  gcphcp ops describe deployment my-deploy -n kube-system

  # Describe a hosted cluster
  gcphcp ops describe hc my-hc -n clusters

  # Describe a node (cluster-scoped, no namespace needed)
  gcphcp ops describe nodes gke-node-abc123`,

		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resourceType := args[0]
			resourceName := args[1]

			if expanded, ok := resourceTypeExpand[resourceType]; ok {
				resourceType = expanded
			}

			project, _ := cmd.Flags().GetString("project")
			region, _ := cmd.Flags().GetString("region")
			outputFormat, _ := cmd.Flags().GetString("output")

			if project == "" {
				return fmt.Errorf("--project is required (or set GCPHCP_PROJECT)")
			}
			if region == "" {
				return fmt.Errorf("--region is required (or set GCPHCP_REGION)")
			}

			data := map[string]interface{}{
				"resource_type": resourceType,
				"name":          resourceName,
			}
			if namespace != "" {
				data["namespace"] = namespace
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			client, err := workflows.NewClient(ctx, project, region)
			if err != nil {
				return fmt.Errorf("creating client: %w", err)
			}
			defer client.Close()

			if err := checkPAMGate(ctx, client, "describe", cmd, os.Stderr); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Describing %s %s", resourceType, resourceName)
			if namespace != "" {
				fmt.Fprintf(os.Stderr, " (ns: %s)", namespace)
			}
			fmt.Fprintln(os.Stderr)

			_, result, err := client.Run(ctx, "describe", data)
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

			printDescribeText(result.Result)
			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Kubernetes namespace")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Maximum time to wait for workflow completion")

	return cmd
}

func printDescribeText(data map[string]interface{}) {
	resource, ok := data["resource"].(map[string]interface{})
	if !ok {
		_ = output.PrintJSON(os.Stdout, data)
		return
	}

	meta := output.AsMap(resource["metadata"])
	spec := output.AsMap(resource["spec"])
	status := output.AsMap(resource["status"])

	isPod := false
	if containers, ok := spec["containers"].([]interface{}); ok && len(containers) > 0 {
		isPod = true
	}

	fmt.Fprintf(os.Stdout, "Name:              %s\n", output.GetString(meta, "name"))
	if ns := output.GetString(meta, "namespace"); ns != "" {
		fmt.Fprintf(os.Stdout, "Namespace:         %s\n", ns)
	}

	if isPod {
		printPodDescribe(meta, spec, status)
	} else {
		printGenericDescribe(meta, spec, status)
	}

	printConditions(data)
	printEvents(data)
}

func printPodDescribe(meta, spec, status map[string]interface{}) {
	if sa := output.GetString(spec, "serviceAccountName"); sa != "" {
		fmt.Fprintf(os.Stdout, "Service Account:   %s\n", sa)
	}
	if node := output.GetString(spec, "nodeName"); node != "" {
		fmt.Fprintf(os.Stdout, "Node:              %s\n", node)
	}
	if startTime := output.GetString(status, "startTime"); startTime != "" {
		fmt.Fprintf(os.Stdout, "Start Time:        %s\n", startTime)
	}

	printLabelsAndAnnotations(meta)

	fmt.Fprintf(os.Stdout, "Status:            %s\n", output.GetString(status, "phase"))
	if podIP := output.GetString(status, "podIP"); podIP != "" {
		fmt.Fprintf(os.Stdout, "IP:                %s\n", podIP)
	}
	if hostIP := output.GetString(status, "hostIP"); hostIP != "" {
		fmt.Fprintf(os.Stdout, "Node IP:           %s\n", hostIP)
	}

	if initContainers, ok := spec["initContainers"].([]interface{}); ok && len(initContainers) > 0 {
		initStatuses, _ := status["initContainerStatuses"].([]interface{})
		fmt.Fprintln(os.Stdout, "\nInit Containers:")
		for _, ic := range initContainers {
			icSpec := output.AsMap(ic)
			name := output.GetString(icSpec, "name")
			icStatus := findContainerStatus(initStatuses, name)
			printContainerDetail(icSpec, icStatus)
		}
	}

	if containers, ok := spec["containers"].([]interface{}); ok && len(containers) > 0 {
		containerStatuses, _ := status["containerStatuses"].([]interface{})
		fmt.Fprintln(os.Stdout, "\nContainers:")
		for _, c := range containers {
			cSpec := output.AsMap(c)
			name := output.GetString(cSpec, "name")
			cStatus := findContainerStatus(containerStatuses, name)
			printContainerDetail(cSpec, cStatus)
		}
	}

	if volumes, ok := spec["volumes"].([]interface{}); ok && len(volumes) > 0 {
		fmt.Fprintln(os.Stdout, "\nVolumes:")
		limit := len(volumes)
		if limit > 5 {
			limit = 5
		}
		for _, v := range volumes[:limit] {
			vm := output.AsMap(v)
			name := output.GetString(vm, "name")
			volType := volumeType(vm)
			fmt.Fprintf(os.Stdout, "  %s:\n", name)
			fmt.Fprintf(os.Stdout, "    Type:    %s\n", volType)
		}
		if len(volumes) > 5 {
			fmt.Fprintf(os.Stdout, "  ... and %d more volumes\n", len(volumes)-5)
		}
	}
}

func printGenericDescribe(meta, spec, status map[string]interface{}) {
	if created := output.GetString(meta, "creationTimestamp"); created != "" {
		fmt.Fprintf(os.Stdout, "Created:           %s\n", created)
	}

	printLabelsAndAnnotations(meta)

	if phase := output.GetString(status, "phase"); phase != "" {
		fmt.Fprintf(os.Stdout, "Status:            %s\n", phase)
	}

	_ = spec
}

func printLabelsAndAnnotations(meta map[string]interface{}) {
	if labels, ok := meta["labels"].(map[string]interface{}); ok && len(labels) > 0 {
		fmt.Fprintln(os.Stdout, "Labels:")
		for k, v := range labels {
			fmt.Fprintf(os.Stdout, "                   %s=%v\n", k, v)
		}
	} else {
		fmt.Fprintln(os.Stdout, "Labels:            <none>")
	}
	if annotations, ok := meta["annotations"].(map[string]interface{}); ok {
		fmt.Fprintf(os.Stdout, "Annotations:       %d\n", len(annotations))
	}
}

func printContainerDetail(spec, status map[string]interface{}) {
	name := output.GetString(spec, "name")
	image := output.GetString(spec, "image")
	if idx := strings.Index(image, "@"); idx > 0 {
		image = image[:idx]
	}

	fmt.Fprintf(os.Stdout, "  %s:\n", name)
	fmt.Fprintf(os.Stdout, "    Image:          %s\n", image)

	if len(status) > 0 {
		state := output.AsMap(status["state"])
		printContainerState("    State:          ", state)

		if lastState := output.AsMap(status["lastState"]); len(lastState) > 0 {
			if terminated := output.AsMap(lastState["terminated"]); len(terminated) > 0 {
				fmt.Fprintf(os.Stdout, "    Last State:     Terminated\n")
				if reason := output.GetString(terminated, "reason"); reason != "" {
					fmt.Fprintf(os.Stdout, "      Reason:       %s\n", reason)
				}
				fmt.Fprintf(os.Stdout, "      Exit Code:    %v\n", terminated["exitCode"])
				if finished := output.GetString(terminated, "finishedAt"); finished != "" {
					fmt.Fprintf(os.Stdout, "      Finished:     %s\n", finished)
				}
			}
		}

		fmt.Fprintf(os.Stdout, "    Ready:          %v\n", status["ready"])
		fmt.Fprintf(os.Stdout, "    Restart Count:  %v\n", status["restartCount"])
	} else {
		fmt.Fprintln(os.Stdout, "    State:          Unknown (no status)")
	}

	if ports, ok := spec["ports"].([]interface{}); ok && len(ports) > 0 {
		var portStrs []string
		for _, p := range ports {
			pm := output.AsMap(p)
			proto := output.GetString(pm, "protocol")
			if proto == "" {
				proto = "TCP"
			}
			portStrs = append(portStrs, fmt.Sprintf("%v/%s", pm["containerPort"], proto))
		}
		fmt.Fprintf(os.Stdout, "    Ports:          %s\n", strings.Join(portStrs, ", "))
	}

	if resources := output.AsMap(spec["resources"]); len(resources) > 0 {
		if limits := output.AsMap(resources["limits"]); len(limits) > 0 {
			fmt.Fprintf(os.Stdout, "    Limits:         %s\n", formatResourceMap(limits))
		}
		if requests := output.AsMap(resources["requests"]); len(requests) > 0 {
			fmt.Fprintf(os.Stdout, "    Requests:       %s\n", formatResourceMap(requests))
		}
	}
}

func printContainerState(prefix string, state map[string]interface{}) {
	if waiting := output.AsMap(state["waiting"]); len(waiting) > 0 {
		fmt.Fprintf(os.Stdout, "%sWaiting\n", prefix)
		if reason := output.GetString(waiting, "reason"); reason != "" {
			fmt.Fprintf(os.Stdout, "      Reason:       %s\n", reason)
		}
		if msg := output.GetString(waiting, "message"); msg != "" {
			if len(msg) > 80 {
				msg = msg[:80]
			}
			fmt.Fprintf(os.Stdout, "      Message:      %s\n", msg)
		}
	} else if running := output.AsMap(state["running"]); len(running) > 0 {
		fmt.Fprintf(os.Stdout, "%sRunning\n", prefix)
		if started := output.GetString(running, "startedAt"); started != "" {
			fmt.Fprintf(os.Stdout, "      Started:      %s\n", started)
		}
	} else if terminated := output.AsMap(state["terminated"]); len(terminated) > 0 {
		fmt.Fprintf(os.Stdout, "%sTerminated\n", prefix)
		if reason := output.GetString(terminated, "reason"); reason != "" {
			fmt.Fprintf(os.Stdout, "      Reason:       %s\n", reason)
		}
		fmt.Fprintf(os.Stdout, "      Exit Code:    %v\n", terminated["exitCode"])
	} else {
		fmt.Fprintf(os.Stdout, "%sUnknown\n", prefix)
	}
}

func findContainerStatus(statuses []interface{}, name string) map[string]interface{} {
	for _, s := range statuses {
		sm := output.AsMap(s)
		if output.GetString(sm, "name") == name {
			return sm
		}
	}
	return nil
}

func volumeType(vm map[string]interface{}) string {
	if cm := output.AsMap(vm["configMap"]); len(cm) > 0 {
		return fmt.Sprintf("ConfigMap (%s)", output.GetString(cm, "name"))
	}
	if sec := output.AsMap(vm["secret"]); len(sec) > 0 {
		return fmt.Sprintf("Secret (%s)", output.GetString(sec, "secretName"))
	}
	if _, ok := vm["emptyDir"]; ok {
		return "EmptyDir"
	}
	if hp := output.AsMap(vm["hostPath"]); len(hp) > 0 {
		return fmt.Sprintf("HostPath (%s)", output.GetString(hp, "path"))
	}
	if pvc := output.AsMap(vm["persistentVolumeClaim"]); len(pvc) > 0 {
		return fmt.Sprintf("PVC (%s)", output.GetString(pvc, "claimName"))
	}
	if _, ok := vm["projected"]; ok {
		return "Projected"
	}
	return "Other"
}

func formatResourceMap(m map[string]interface{}) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s: %v", k, v))
	}
	return strings.Join(parts, ", ")
}

func printConditions(data map[string]interface{}) {
	conditions, ok := data["conditions"].([]interface{})
	if !ok || len(conditions) == 0 {
		return
	}
	fmt.Fprintln(os.Stdout, "\nConditions:")
	for _, c := range conditions {
		cm := output.AsMap(c)
		line := fmt.Sprintf("  %s: %s", output.GetString(cm, "type"), output.GetString(cm, "status"))
		if reason := output.GetString(cm, "reason"); reason != "" {
			line += fmt.Sprintf(" (%s)", reason)
		}
		if msg := output.GetString(cm, "message"); msg != "" && len(msg) < 50 {
			line += fmt.Sprintf(" - %s", msg)
		}
		fmt.Fprintln(os.Stdout, line)
	}
}

func printEvents(data map[string]interface{}) {
	events, ok := data["events"].(map[string]interface{})
	if !ok {
		return
	}
	items, _ := events["items"].([]interface{})
	fmt.Fprintln(os.Stdout)
	if len(items) == 0 {
		fmt.Fprintln(os.Stdout, "Events:            <none>")
		return
	}
	fmt.Fprintln(os.Stdout, "Events:")
	t := output.NewTable(os.Stdout, "AGE", "TYPE", "REASON", "MESSAGE")
	for _, item := range items {
		ev := output.AsMap(item)
		lastTimestamp := output.GetString(ev, "lastTimestamp")
		if lastTimestamp == "" {
			lastTimestamp = output.GetString(ev, "eventTime")
		}
		msg := output.GetString(ev, "message")
		if len(msg) > 70 {
			msg = msg[:70]
		}
		t.AddRow(
			output.Age(lastTimestamp),
			output.GetString(ev, "type"),
			output.GetString(ev, "reason"),
			msg,
		)
	}
	_ = t.Flush()
}
