package application

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
)

func absentAPI(err error) bool {
	return platformapi.IsResourceNotFound(err)
}

// TeardownGuard never deletes a Cluster. It trusts normal finalizer completion,
// then independently checks namespace references and customer cloud dependants.
// The API has no demonstrated atomic cross-resource delete lock; these reads do
// not protect against another authorized actor racing a new manual installation.
type TeardownGuard struct {
	Client       *platformapi.Client
	NoDependents func(context.Context, *Plan) error
	Wait         func(context.Context) error // test clock seam; production waits 30 seconds.
}

func (g TeardownGuard) Check(ctx context.Context, j *Journal) error {
	if g.Client == nil || g.NoDependents == nil {
		return fmt.Errorf("authenticated platform and cloud teardown inspection are required")
	}
	if g.Client.Project() != j.Plan.Identity.Project {
		return fmt.Errorf("teardown lookup project mismatch")
	}
	if err := CheckRealm(ctx, g.Client, &j.Plan); err != nil {
		return err
	}
	if j.Cluster.State == "Intent" || j.Cluster.State == "Unknown" {
		return fmt.Errorf("uncertain Cluster submission cannot be inferred absent from polling")
	}
	if j.Cluster.State != "None" {
		if j.Cluster.State != "Confirmed" || j.Cluster.UID == "" || j.Cluster.Namespace != g.Client.Namespace() {
			return fmt.Errorf("confirmed Cluster identity is required for teardown proof")
		}
		for n := 0; n < 2; n++ {
			if _, err := g.Client.Clusters().Get(ctx, j.Cluster.Namespace, j.Cluster.Name); !absentAPI(err) {
				if err != nil {
					return err
				}
				return fmt.Errorf("Cluster still exists or is deleting; finish normal hosted teardown first")
			}
			if n == 0 {
				if g.Wait != nil {
					if err := g.Wait(ctx); err != nil {
						return err
					}
				} else {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(30 * time.Second):
					}
				}
			}
		}
	}
	clusters, err := g.Client.Clusters().List(ctx, g.Client.Namespace())
	if err != nil {
		return err
	}
	if clusters.Continue != "" {
		return fmt.Errorf("complete namespace inventory is required for cleanup")
	}
	for _, c := range clusters.Items {
		if c.Spec.InfraID == j.Plan.Identity.InfraID || (c.Spec.Platform.GCP != nil && c.Spec.Platform.GCP.WorkloadIdentity.PoolID == j.Plan.Identity.InfraID+"-wi-pool") {
			return fmt.Errorf("a Cluster still references the IAM installation")
		}
	}
	if j.Cluster.Name != "" {
		pools, err := g.Client.NodePools().List(ctx, g.Client.Namespace())
		if err != nil {
			return err
		}
		if pools.Continue != "" {
			return fmt.Errorf("complete NodePool inventory is required for cleanup")
		}
		for _, p := range pools.Items {
			if p.Spec.ClusterID == j.Cluster.Name {
				return fmt.Errorf("a NodePool still references the submitted Cluster")
			}
		}
	}
	return g.NoDependents(ctx, &j.Plan)
}
