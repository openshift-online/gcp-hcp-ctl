# gcp-hcp-ctl

CLI for managing GCP Hosted Control Plane (HCP) clusters.

Part of the [GCP HCP](https://github.com/openshift-online/gcp-hcp) project. See [design decisions](https://github.com/openshift-online/gcp-hcp/tree/main/design-decisions) for architectural context.

## Quick Start

```bash
# Build
make build

# Configure (project and region are required)
mkdir -p ~/.gcphcpctl
cat > ~/.gcphcpctl/config.yaml << EOF
project: your-gcp-project-id
region: us-central1
EOF

# Or use flags / env vars instead
export GCPHCPCTL_PROJECT=your-gcp-project-id
export GCPHCPCTL_REGION=us-central1
```

## Commands

### IAM Infrastructure for Hosted Clusters (`iam`)

Create and destroy Workload Identity Federation (WIF) infrastructure for
HyperShift clusters, including WIF pools, OIDC providers, and Google Service
Accounts with IAM role bindings.

```bash
# Create IAM infrastructure for a cluster
gcphcpctl iam create <infra-id> --oidc-issuer-url https://oidc.example.com/my-cluster
gcphcpctl iam create <infra-id> --oidc-jwks-file /path/to/jwks.json
gcphcpctl iam create <infra-id> --oidc-issuer-url https://oidc.example.com --output-file iam-output.json

# Destroy IAM infrastructure for a cluster
gcphcpctl iam destroy <infra-id>
gcphcpctl iam destroy <infra-id> --yes    # skip confirmation prompt
```

All create operations are idempotent (safe to run multiple times). Destroy
operations tolerate not-found errors gracefully.

### Operational Debugging (`ops`)

Convenience wrappers that run Cloud Workflows to interact with GKE clusters
without direct cluster access (Zero Operator Access).

```bash
# Get resources (kubectl-style)
gcphcpctl ops get pods -n hypershift
gcphcpctl ops get nodes
gcphcpctl ops get deployments -n kube-system
gcphcpctl ops get hc -n clusters               # aliases: hc, hcp, np, deploy, svc, etc.

# Get raw JSON response (full Kubernetes API output)
gcphcpctl ops get pods -n hypershift -o json

# AI-powered pod analysis (uses Vertex AI to diagnose issues from logs/events)
gcphcpctl ops get pods my-pod -n hypershift --analyze

# Pod logs
gcphcpctl ops logs my-pod -n hypershift
gcphcpctl ops logs my-pod -n hypershift -c etcd --tail 50

# Describe resources
gcphcpctl ops describe pods my-pod -n hypershift
gcphcpctl ops describe deployment my-deploy -n kube-system

# Delete resources (pods, jobs, deployments)
gcphcpctl ops delete pods my-pod -n clusters-abc123
gcphcpctl ops delete pods my-pod -n clusters-abc123 --grace-period 0

# Expand PVC storage
gcphcpctl ops expand-volume data-etcd-0 -n clusters-abc123 --size 20Gi

# etcd operations
gcphcpctl ops etcd health -n clusters-abc123
gcphcpctl ops etcd status -n clusters-abc123
gcphcpctl ops etcd member-list -n clusters-abc123
gcphcpctl ops etcd defrag -n clusters-abc123
```

### Workflow Management (`ops wf`)

Direct Cloud Workflow management for arbitrary workflow execution.

```bash
# List deployed workflows
gcphcpctl ops wf list

# List execution history for a workflow
gcphcpctl ops wf list get --limit 5

# Run a workflow
gcphcpctl ops wf run get --data '{"resource_type": "pods", "namespace": "hypershift"}'

# Run async (returns immediately)
gcphcpctl ops wf run describe --data '{"resource_type": "pods", "name": "etcd-0"}' --async

# Check execution status
gcphcpctl ops wf status get <execution-id>

# Resume a paused workflow (callback)
gcphcpctl ops wf resume approval-flow <execution-id> --data '{"approved": true}'
```

## Configuration

Configuration priority: **CLI flags > environment variables > config file**.

| Flag | Env Var | Config Key | Description |
|------|---------|------------|-------------|
| `--project` | `GCPHCPCTL_PROJECT` | `project` | GCP project ID (required) |
| `--region` | `GCPHCPCTL_REGION` | `region` | GCP region (required) |
| `--output` / `-o` | - | `output` | Output format: `text`, `json` |

Config file location: `~/.gcphcpctl/config.yaml`

## Project Structure

```
cmd/gcphcpctl/        Entry point for the gcphcpctl binary
pkg/
├── cli/              Root command, version, completion
├── infra/
│   └── iam/          IAM infrastructure orchestration and CLI commands
├── ops/              Operational commands (extractable as plugin)
│   └── wf/           Workflow management subcommands
├── gcp/
│   ├── iam/          Pure GCP IAM API client wrappers
│   └── workflows/    Cloud Workflows API client
├── config/           Config file loading
└── output/           Table and JSON output formatting
hack/workflows/       Cloud Workflow YAML definitions
```

## Development

```bash
make build    # Build bin/gcphcpctl
make test     # Run unit tests
make lint     # Run go vet
make clean    # Remove build artifacts
```

### Prerequisites

- Go 1.24+
- GCP credentials: `gcloud auth application-default login`
- Cloud Workflows deployed in the target project/region

## Architecture

The CLI currently has the following command categories:

- **Infrastructure commands** (`iam`, `network`): Provision and tear down GCP
  resources for HyperShift clusters. These live under `pkg/infra/` for
  orchestration logic and `pkg/gcp/` for pure GCP API client wrappers. The
  separation keeps API clients reusable and side-effect-free while command
  orchestration handles retries, ordering, and user interaction.

- **Operational commands** (`ops`): Debug and remediate running clusters via
  Cloud Workflows (Zero Operator Access pattern). The `ops` subtree is
  self-contained under `pkg/ops/` with no dependencies on `pkg/cli/`, allowing
  extraction into a standalone plugin binary (`gcphcpctl-ops`). A stub entry
  point exists at `cmd/ops/main.go` for when that separation is needed.

All commands inherit global `--project` and `--region` flags from the root
command. Configuration follows the priority: CLI flags > environment variables
> config file.

## Related Repositories

- [gcp-hcp](https://github.com/openshift-online/gcp-hcp) - Design decisions and architecture
- [gcp-hcp-infra](https://github.com/openshift-online/gcp-hcp-infra) - Terraform infrastructure and ArgoCD configuration
