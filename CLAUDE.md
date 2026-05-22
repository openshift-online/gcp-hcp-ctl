# CLAUDE.md

This file provides guidance to AI agents when working with code in this repository.

## Repository Overview

`gcp-hcp-ctl` is a Go CLI for managing GCP Hosted Control Plane (HCP) clusters. It provides operational debugging tools that communicate with GKE clusters exclusively through Cloud Workflows (Zero Operator Access pattern), plus workflow management commands.

**Module path**: `github.com/openshift-online/gcp-hcp-ctl`

## Development Commands

```bash
make build    # Build bin/gcphcpctl
make test     # Run unit tests with race detection
make lint     # Run go vet
make clean    # Remove build artifacts
```

## Project Structure

```
cmd/
├── gcphcp/           # Main CLI entry point
└── ops/              # Standalone plugin entry point (future extraction)
pkg/
├── cli/              # Root command, version, completion
├── config/           # Config file loading (~/.gcphcpctl/config.yaml)
├── ops/              # Operational commands (self-contained, extractable)
│   ├── companion/    # AI companion (PagerDuty, tools, sessions)
│   ├── pam/          # Privileged Access Manager commands
│   └── wf/           # Cloud Workflow management subcommands
├── gcp/
│   ├── auditlog/     # Cloud Audit Log client
│   ├── cloudrun/     # Cloud Run client
│   ├── pam/          # PAM API client
│   └── workflows/    # Cloud Workflows API client + callbacks
└── output/           # Table and JSON output formatting
hack/workflows/       # Cloud Workflow YAML definitions
```

## Architecture

The `ops` subtree is self-contained under `pkg/ops/` with no dependencies on `pkg/cli/`. This allows extraction into a standalone plugin binary (`gcphcpctl-ops`). A stub entry point exists at `cmd/ops/main.go`.

All cluster interactions go through Cloud Workflows (Zero Operator Access). Workflows are deployed to the management cluster's GCP project and use the GKE API with Workload Identity.

## Code Conventions

- Go 1.24+ required
- GCP credentials via `gcloud auth application-default login`
- Configuration priority: CLI flags > environment variables > config file (`~/.gcphcpctl/config.yaml`)
- Version info injected via `-ldflags` at build time (see `Makefile`)

## Testing

```bash
make test     # Runs go test -race ./...
```

Tests use standard Go testing with table-driven patterns. Mock GCP clients are used for unit tests.

## Security

- No hardcoded credentials; all auth via Application Default Credentials or Workload Identity
- Cloud Workflows provide an auditable, controlled access layer to clusters
- PAM integration enforces just-in-time privileged access
