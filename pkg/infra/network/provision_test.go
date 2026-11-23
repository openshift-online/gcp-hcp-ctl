package network

import (
	"context"
	"fmt"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
	"google.golang.org/api/googleapi"
)

type fakeOwnedCloud struct {
	resources map[string]*Observed
	writes    int
}

func (f *fakeOwnedCloud) Read(_ context.Context, p OwnedPlan, kind, owner string) (*Observed, error) {
	v, ok := f.resources[kind]
	if !ok {
		return nil, &googleapi.Error{Code: 404}
	}
	copy := *v
	return &copy, nil
}
func (f *fakeOwnedCloud) Start(_ context.Context, p OwnedPlan, kind, owner string) (string, error) {
	f.writes++
	f.resources[kind] = &Observed{ID: fmt.Sprint(f.writes), Owner: expectedOwner(p, kind, owner), Matches: true}
	return "operation-" + kind, nil
}
func (f *fakeOwnedCloud) Wait(context.Context, string, string) error { return nil }
func (f *fakeOwnedCloud) TargetID(_ context.Context, _ OwnedPlan, kind, _ string) (string, error) {
	return f.resources[kind].ID, nil
}

func TestOwnedNetworkPreviewRefusesForeignAndPinsExternalInputs(t *testing.T) {
	p := OwnedPlan{Project: "test-project", Region: "us-central1", InfraID: "fresh-123", Version: "5.0.0", CIDR: DefaultSubnetCIDR}
	f := &fakeOwnedCloud{resources: map[string]*Observed{}}
	if err := InspectOwned(t.Context(), f, p, nil); err != nil || f.writes != 0 {
		t.Fatal("preview failed or mutated cloud")
	}
	for _, kind := range p.Kinds() {
		f.resources[kind] = &Observed{ID: kind + "-uid", Owner: "external", Matches: true}
	}
	if err := InspectOwned(t.Context(), f, p, nil); err == nil || f.writes != 0 {
		t.Fatal("existing network was adopted")
	}
	observed, err := InspectExternal(t.Context(), f, p, nil)
	if err != nil || len(observed) != 3 || f.writes != 0 {
		t.Fatal("explicit external config was not read-only")
	}
	j := &application.Journal{Steps: map[string]*application.Step{}}
	for kind, v := range observed {
		j.Steps["network/"+kind] = &application.Step{State: "External", Resource: &application.Resource{UID: v.ID}}
	}
	f.resources["Network"].ID = "recreated"
	if _, err := InspectExternal(t.Context(), f, p, j); err == nil {
		t.Fatal("recreated external network accepted")
	}
}

func TestOwnedNetworkPlanPreservesFirewallVersionPolicy(t *testing.T) {
	p := OwnedPlan{Project: "test-project", Region: "us-central1", InfraID: "fresh-123", Version: "5.0.0", CIDR: DefaultSubnetCIDR}
	if len(p.Kinds()) != 3 || p.Output().FirewallRuleName != "" {
		t.Fatal("GA should retain CPO-owned firewall semantics")
	}
	p.Version = "5.0.0-rc.8"
	if len(p.Kinds()) != 4 || p.Output().FirewallRuleName != "fresh-123-internal-cluster" {
		t.Fatal("prerelease firewall policy changed")
	}
}
