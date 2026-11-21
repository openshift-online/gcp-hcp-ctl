package network

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/api/compute/v1"
)

func journalOwned(description string) bool { return strings.HasPrefix(description, "gcphcpctl/v1/") }

type legacyNetworkReader interface {
	GetNetwork(context.Context, string) (*compute.Network, error)
	GetSubnet(context.Context, string) (*compute.Subnetwork, error)
	GetRouter(context.Context, string) (*compute.Router, error)
}

// Unmarked historical stacks retain their existing commands. Never let those
// name-only commands mutate a new journal-managed stack or bypass teardown proof.
func guardLegacyDestruction(ctx context.Context, c legacyNetworkReader, infra string) error {
	checks := []func() (string, error){func() (string, error) {
		v, err := c.GetNetwork(ctx, infra+"-network")
		if err != nil {
			return "", err
		}
		return v.Description, nil
	}, func() (string, error) {
		v, err := c.GetSubnet(ctx, infra+"-subnet")
		if err != nil {
			return "", err
		}
		return v.Description, nil
	}, func() (string, error) {
		v, err := c.GetRouter(ctx, infra+"-router")
		if err != nil {
			return "", err
		}
		return v.Description, nil
	}}
	for _, check := range checks {
		owner, err := check()
		if isNotFoundError(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot establish legacy network ownership; no deletion attempted")
		}
		if journalOwned(owner) {
			return fmt.Errorf("journal-managed installation requires the owned state-file cleanup flow")
		}
	}
	return nil
}
