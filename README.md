# gcphcp

CLI for managing GCP Hosted Control Plane (HCP) clusters.

## Quick Start

```bash
# Build
make build

# Configure (project and region are required)
mkdir -p ~/.gcphcp
cat > ~/.gcphcp/config.yaml << EOF
project: your-gcp-project-id
region: us-central1
EOF

# Or use flags / env vars instead
export GCPHCP_PROJECT=your-gcp-project-id
export GCPHCP_REGION=us-central1
```

## Commands

### Operational Debugging (`ops`)

Convenience wrappers that run Cloud Workflows to interact with GKE clusters
without direct cluster access (Zero Operator Access).

```bash
# Get resources (kubectl-style)
gcphcp ops get pods -n hypershift
gcphcp ops get nodes
gcphcp ops get deployments -n kube-system
gcphcp ops get hc -n clusters               # aliases: hc, hcp, np, deploy, svc, etc.

# Get raw JSON response (full Kubernetes API output)
gcphcp ops get pods -n hypershift -o json

# AI-powered pod analysis (uses Vertex AI to diagnose issues from logs/events)
gcphcp ops get pods my-pod -n hypershift --analyze

# Pod logs
gcphcp ops logs my-pod -n hypershift
gcphcp ops logs my-pod -n hypershift -c etcd --tail 50

# Describe resources
gcphcp ops describe pods my-pod -n hypershift
gcphcp ops describe deployment my-deploy -n kube-system

# Delete resources (pods, jobs, deployments)
gcphcp ops delete pods my-pod -n clusters-abc123
gcphcp ops delete pods my-pod -n clusters-abc123 --grace-period 0

# Expand PVC storage
gcphcp ops expand-volume data-etcd-0 -n clusters-abc123 --size 20Gi

# etcd operations
gcphcp ops etcd health -n clusters-abc123
gcphcp ops etcd status -n clusters-abc123
gcphcp ops etcd member-list -n clusters-abc123
gcphcp ops etcd defrag -n clusters-abc123
```

### Workflow Management (`ops wf`)

Direct Cloud Workflow management for arbitrary workflow execution.

```bash
# List deployed workflows
gcphcp ops wf list

# List execution history for a workflow
gcphcp ops wf list get --limit 5

# Run a workflow
gcphcp ops wf run get --data '{"resource_type": "pods", "namespace": "hypershift"}'

# Run async (returns immediately)
gcphcp ops wf run describe --data '{"resource_type": "pods", "name": "etcd-0"}' --async

# Check execution status
gcphcp ops wf status get <execution-id>

# Resume a paused workflow (callback)
gcphcp ops wf resume approval-flow <execution-id> --data '{"approved": true}'
```

## Configuration

Configuration priority: **CLI flags > environment variables > config file**.

| Flag | Env Var | Config Key | Description |
|------|---------|------------|-------------|
| `--project` | `GCPHCP_PROJECT` | `project` | GCP project ID (required) |
| `--region` | `GCPHCP_REGION` | `region` | GCP region (required) |
| `--output` / `-o` | - | `output` | Output format: `text`, `json` |

Config file location: `~/.gcphcp/config.yaml`

## Project Structure

```
cmd/gcphcp/           Entry point for the gcphcp binary
pkg/
├── cli/              Root command, version, completion
├── ops/              Operational commands (extractable as plugin)
│   └── wf/           Workflow management subcommands
├── gcp/
│   └── workflows/    Cloud Workflows API client
├── config/           Config file loading
└── output/           Table and JSON output formatting
hack/workflows/       Cloud Workflow YAML definitions
```

## Development

```bash
make build    # Build bin/gcphcp
make test     # Run unit tests
make lint     # Run go vet
make clean    # Remove build artifacts
```

### Prerequisites

- Go 1.24+
- GCP credentials: `gcloud auth application-default login`
- Cloud Workflows deployed in the target project/region

## Architecture

The `ops` subtree is self-contained under `pkg/ops/` with no dependencies on
`pkg/cli/`. This allows it to be extracted into a standalone plugin binary
(`gcphcp-ops`) in the future. A stub entry point exists at `cmd/ops/main.go`
for when that separation is needed.

The CLI communicates with GKE clusters exclusively through Cloud Workflows
(Zero Operator Access pattern). The workflows are deployed to the management
cluster's GCP project and use the GKE API with Workload Identity for
authentication.
