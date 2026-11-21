package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/network"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (o *createOptions) run(cmd *cobra.Command, name string) error {
	ctx := cmd.Context()
	client := clientFromCmd(cmd)
	region, _ := cmd.Flags().GetString("region")
	if region == "" {
		region = "us-central1"
	}
	if o.version == "" || o.channelGroup == "" {
		return fmt.Errorf("an exact --version and channel group are required")
	}
	if o.endpointAccess != "Private" && o.endpointAccess != "PublicAndPrivate" {
		return fmt.Errorf("unsupported endpoint access")
	}
	if !o.dryRun && (!o.confirm || o.stateFile == "" || o.expectPlan == "") {
		return fmt.Errorf("installation requires --confirm, --state-file and a matching --expect-plan preview")
	}
	if o.setupInfra && (o.iamConfigFile != "" || o.networkConfigFile != "") {
		return fmt.Errorf("setup-infra and config-file modes are mutually exclusive")
	}
	var hints *iam.CreateOutput
	var netHints *network.CreateOutput
	infra := o.infraID
	if !o.setupInfra {
		if o.iamConfigFile == "" || o.networkConfigFile == "" || o.stateFile == "" {
			return fmt.Errorf("config-file mode requires both configs and the owned --state-file")
		}
		var err error
		hints = &iam.CreateOutput{}
		netHints = &network.CreateOutput{}
		if err = readConfig(o.iamConfigFile, hints); err != nil {
			return err
		}
		if err = readConfig(o.networkConfigFile, netHints); err != nil {
			return err
		}
		if hints.SchemaVersion != 1 || hints.TemplateRef == nil || hints.OperationID == "" {
			return fmt.Errorf("legacy IAM config cannot establish cloud verification or ownership")
		}
		if hints.ProjectID != client.Project() || netHints.ProjectID != client.Project() || hints.InfraID != netHints.InfraID || (infra != "" && infra != hints.InfraID) || netHints.Region != region {
			return fmt.Errorf("flag/config project, region or installation identity conflict")
		}
		infra = hints.InfraID
	}
	if infra == "" {
		if !o.dryRun {
			return fmt.Errorf("confirmed setup requires the previewed --infra-id")
		}
		var err error
		infra, err = generateCompliantInfraID(name)
		if err != nil {
			return err
		}
	}
	if err := validateInfraID(infra); err != nil {
		return err
	}
	issuerBase := strings.TrimRight(regionInfoFromCmd(cmd).OIDCIssuer, "/")
	if issuerBase == "" {
		return fmt.Errorf("discovered OIDC issuer is required")
	}
	// Resolve already-submitted work first. This is a read-only API identity
	// resolution, not a new application authorization after catalog revocation.
	var state *application.Journal
	if o.resume {
		old, err := application.Load(o.stateFile)
		if err != nil && !(errors.Is(err, application.ErrOrphanJournal) && o.setupInfra) {
			return err
		}
		state = old
		if old != nil && old.Cluster.State != "None" {
			if err := application.CheckRealm(ctx, client, &old.Plan); err != nil {
				return err
			}
			if o.setupInfra == old.NetworkExternal {
				return fmt.Errorf("resume mode conflicts with journaled network ownership")
			}
			if old.Plan.Identity.Project != client.Project() || old.Plan.Identity.InfraID != infra || old.Plan.Identity.Issuer != issuerBase+"/"+infra || old.Plan.Target.Version != o.version || old.Plan.Target.ChannelGroup != o.channelGroup || old.Cluster.Name != name || (!o.dryRun && old.InstallationFingerprint != o.expectPlan) {
				return fmt.Errorf("resume request conflicts with the journaled installation")
			}
			if o.dryRun {
				var original v1.Cluster
				if err := json.Unmarshal(old.Cluster.Payload, &original); err != nil {
					return fmt.Errorf("invalid journaled Cluster payload")
				}
				actual, err := client.Clusters().Get(ctx, old.Cluster.Namespace, old.Cluster.Name)
				if err != nil {
					return err
				}
				if err := validateSubmission(old, actual, &original); err != nil {
					return err
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"executable": false, "mutationPlanned": false, "operationID": old.OperationID, "submissionState": old.Cluster.State, "observedUID": actual.UID, "planFingerprint": old.InstallationFingerprint})
			}
			file, err := application.Open(o.stateFile, &old.Plan, true)
			if err != nil {
				return err
			}
			defer file.Close()
			var original v1.Cluster
			if err := json.Unmarshal(file.Journal.Cluster.Payload, &original); err != nil {
				return fmt.Errorf("invalid journaled Cluster payload")
			}
			return o.resolveSubmission(ctx, cmd, client, &application.Session{File: file, Catalog: client}, &original)
		}
	}
	iamOptions := &iam.CreateOptions{ProjectID: client.Project(), InfraID: infra, OIDCIssuerURL: issuerBase + "/" + infra, Version: o.version, ChannelGroup: o.channelGroup, Catalog: client, Cloud: o.iamCloud}
	p, cloud, err := iamOptions.Prepare(ctx)
	if err != nil {
		return err
	}
	np := network.OwnedPlan{Project: client.Project(), InfraID: infra, Region: region, Version: o.version, CIDR: network.DefaultSubnetCIDR}
	if o.subnetCIDR != "" {
		np.CIDR = o.subnetCIDR
	}
	if netHints != nil {
		if cmd.Flags().Changed("vpc-cidr") && o.subnetCIDR != netHints.SubnetCIDR {
			return fmt.Errorf("subnet CIDR flag conflicts with network config")
		}
		np.CIDR = netHints.SubnetCIDR
		if !np.MatchesConfig(netHints) {
			return fmt.Errorf("network config does not identify the selected installation")
		}
	}
	nc := o.networkCloud
	if nc == nil {
		nc, err = network.NewOwnedGCP(ctx, np)
		if err != nil {
			return err
		}
	}
	if (o.resume || !o.setupInfra) && state == nil {
		state, err = application.Load(o.stateFile)
		if err != nil && !(errors.Is(err, application.ErrOrphanJournal) && o.setupInfra) {
			return err
		}
		if state != nil && state.Fingerprint != p.Fingerprint() {
			return fmt.Errorf("journal identity conflicts with current catalog/cloud inputs")
		}
	}
	if hints != nil {
		if hints.OperationID != state.OperationID || *hints.TemplateRef != p.Target.Reference || hints.WorkerServiceAccount != p.Identity.WorkerEmail || !reflect.DeepEqual(hints.ServiceAccounts, p.Accounts()) {
			return fmt.Errorf("IAM config conflicts with independently resolved contract or journal")
		}
		expectedAudience := "//iam.googleapis.com/projects/" + p.Identity.ProjectNumber + "/locations/global/workloadIdentityPools/" + infra + "-wi-pool/providers/" + infra + "-k8s-provider"
		if hints.ProjectNumber != p.Identity.ProjectNumber || hints.WorkloadIdentityPool.PoolID != infra+"-wi-pool" || hints.WorkloadIdentityPool.ProviderID != infra+"-k8s-provider" || hints.WorkloadIdentityPool.Audience != expectedAudience {
			return fmt.Errorf("IAM config project number, federation identity or audience conflicts with current inputs")
		}
	}
	if err := ensureNewInstallation(ctx, client, name, infra); err != nil {
		return err
	}
	if err := (&application.Engine{Cloud: cloud}).Inspect(ctx, p, state); err != nil {
		return err
	}
	if o.setupInfra {
		if err := network.InspectOwned(ctx, nc, np, state); err != nil {
			return err
		}
	} else {
		if _, err := network.InspectExternal(ctx, nc, np, state); err != nil {
			return err
		}
	}
	iamResult := &iam.CreateOutput{ProjectID: p.Identity.Project, ProjectNumber: p.Identity.ProjectNumber, InfraID: p.Identity.InfraID, ServiceAccounts: p.Accounts(), WorkloadIdentityPool: iam.WorkloadIdentityConfig{PoolID: infra + "-wi-pool", ProviderID: infra + "-k8s-provider"}}
	payload, err := assemblePayload(iamResult, np.Output(), buildPayloadOptions{clusterName: name, infraID: infra, projectID: client.Project(), region: region, endpointAccess: o.endpointAccess, oidcEndpoint: issuerBase, version: o.version, channelGroup: o.channelGroup})
	if err != nil {
		return err
	}
	payload.Spec.Platform.GCP.IAM = &v1.ClusterIAMSpec{WorkerServiceAccount: p.Identity.WorkerEmail, TemplateApplications: []v1.WIFConfigTemplateReference{p.Target.Reference}}
	payload.Spec.Networking.MachineNetwork = []v1.MachineNetworkEntry{{CIDR: np.CIDR}}
	if o.maintenanceUntil != "" {
		until, err := time.Parse(time.RFC3339, o.maintenanceUntil)
		now := time.Now().UTC()
		if o.now != nil {
			now = o.now().UTC()
		}
		start := metav1.NewTime(now.Truncate(24 * time.Hour))
		if state != nil && len(state.InstallationPayload) > 0 {
			var prior v1.Cluster
			if err := json.Unmarshal(state.InstallationPayload, &prior); err != nil {
				return fmt.Errorf("invalid prepared installation payload")
			}
			if prior.Spec.Maintenance != nil && prior.Spec.Maintenance.Exclusions != nil && len(*prior.Spec.Maintenance.Exclusions) == 1 {
				ex := (*prior.Spec.Maintenance.Exclusions)[0]
				if ex.EndTime.Time.Equal(until) {
					start = ex.StartTime
				}
			}
		}
		if err != nil || !until.After(now) || until.After(start.Add(30*24*time.Hour)) {
			return fmt.Errorf("maintenance exclusion expiry must be a future RFC3339 time within 30 days of today's UTC midnight")
		}
		payload.Spec.Maintenance = &v1.ClusterMaintenanceSpec{Exclusions: &[]v1.ControlPlaneMaintenanceExclusion{{Name: "initial-installation", StartTime: start, EndTime: metav1.NewTime(until)}}}
	}
	mode := "config-verify"
	if o.setupInfra {
		mode = "setup-infra"
	}
	fullFingerprint := installationFingerprint(p, np, payload, mode)
	if o.dryRun {
		executable := p.Target.Eligible() == nil
		authorityBlock := ""
		if o.setupInfra {
			if authority, ok := cloud.(interface{ CheckWriterAuthority(context.Context) error }); ok {
				if err := authority.CheckWriterAuthority(ctx); err != nil {
					executable = false
					authorityBlock = err.Error()
				}
			}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"executable": executable, "authorityBlock": authorityBlock, "mode": mode, "infraID": infra, "planFingerprint": fullFingerprint, "iamPlanFingerprint": p.Fingerprint(), "iam": p, "network": np, "cluster": payload})
	}
	if o.expectPlan != fullFingerprint {
		return fmt.Errorf("installation plan changed; review a fresh preview")
	}
	if state != nil && !o.resume {
		return fmt.Errorf("existing operation requires --resume")
	}
	session, err := application.Start(ctx, client, cloud, p, o.stateFile, p.Fingerprint(), o.resume)
	if err != nil {
		return err
	}
	defer session.Close()
	j := session.File.Journal
	if j.InstallationFingerprint != "" && j.InstallationFingerprint != fullFingerprint {
		return fmt.Errorf("the journal already binds a different complete installation plan")
	}
	j.InstallationFingerprint = fullFingerprint
	j.NetworkSpec, _ = json.Marshal(np)
	j.InstallationPayload, _ = json.Marshal(payload)
	if err := session.File.Save(j); err != nil {
		return err
	}
	if j.Cluster.State != "None" {
		return o.resolveSubmission(ctx, cmd, client, session, payload)
	}
	if o.setupInfra {
		if err := session.Apply(ctx); err != nil {
			return err
		}
	} else {
		if err := session.Verify(ctx); err != nil {
			return err
		}
	}
	if o.setupInfra {
		if err := network.ProvisionOwned(ctx, nc, np, session); err != nil {
			return err
		}
	} else {
		observed, err := network.InspectExternal(ctx, nc, np, j)
		if err != nil {
			return err
		}
		if j.NetworkFingerprint != "" && !j.NetworkExternal {
			return fmt.Errorf("an owned setup operation cannot be converted into external-network ownership")
		}
		j.NetworkExternal = true
		j.NetworkFingerprint = np.Fingerprint()
		for kind, v := range observed {
			j.Steps["network/"+kind] = &application.Step{State: "External", Resource: &application.Resource{Kind: kind, Name: np.Name(kind), UID: v.ID}}
		}
		j.Phase = "NetworkVerified"
		if err := session.File.Save(j); err != nil {
			return err
		}
	}
	if err := session.Verify(ctx); err != nil {
		return err
	}
	if o.setupInfra {
		if err := network.InspectOwned(ctx, nc, np, j); err != nil {
			return err
		}
	} else {
		if _, err := network.InspectExternal(ctx, nc, np, j); err != nil {
			return err
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("cannot encode initial Cluster payload")
	}
	if err := ensureNewInstallation(ctx, client, name, infra); err != nil {
		return err
	}
	if err := application.Recheck(ctx, client, &p.Target); err != nil {
		return err
	}
	j.Cluster = application.Submission{State: "Intent", Namespace: client.Namespace(), Name: name, Payload: data, IntentAt: time.Now().UTC(), PreflightAbsent: true}
	j.Phase = "ClusterIntent"
	if err := session.File.Save(j); err != nil {
		return err
	}
	created, err := client.Clusters().Create(ctx, client.Namespace(), payload)
	if err != nil {
		if definitiveAPI(err) {
			j.Cluster = application.Submission{State: "None"}
			j.Phase = "NetworkVerified"
			if saveErr := session.File.Save(j); saveErr != nil {
				return saveErr
			}
			return err
		}
		j.Cluster.State = "Unknown"
		if saveErr := session.File.Save(j); saveErr != nil {
			return saveErr
		}
		return o.resolveSubmission(ctx, cmd, client, session, payload)
	}
	return o.recordSubmission(cmd, session, created, payload)
}

func readConfig(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read installation config")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("invalid installation config JSON")
	}
	return nil
}
func installationFingerprint(p *application.Plan, n network.OwnedPlan, c *v1.Cluster, mode string) string {
	data, _ := json.Marshal(struct {
		IAM, Network, Mode string
		Cluster            *v1.Cluster
	}{p.Fingerprint(), n.Fingerprint(), mode, c})
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}

func (o *createOptions) resolveSubmission(ctx context.Context, cmd *cobra.Command, client *platformapi.Client, s *application.Session, want *v1.Cluster) error {
	j := s.File.Journal
	var original v1.Cluster
	if err := json.Unmarshal(j.Cluster.Payload, &original); err != nil {
		return fmt.Errorf("journaled Cluster payload is invalid")
	}
	if !apiequality.Semantic.DeepEqual(original.Spec, want.Spec) || j.Cluster.Name != want.Name || j.Cluster.Namespace != client.Namespace() {
		return fmt.Errorf("submission identity changed; refusing journal reuse")
	}
	actual, err := client.Clusters().Get(ctx, j.Cluster.Namespace, j.Cluster.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("Cluster create outcome is still uncertain; no new IAM, name or POST was attempted")
		}
		return err
	}
	return o.recordSubmission(cmd, s, actual, &original)
}
func (o *createOptions) recordSubmission(cmd *cobra.Command, s *application.Session, actual, want *v1.Cluster) error {
	j := s.File.Journal
	if err := validateSubmission(j, actual, want); err != nil {
		return err
	}
	j.Cluster.UID = string(actual.UID)
	j.Cluster.State = "Confirmed"
	j.Phase = "ClusterSubmitted"
	if err := s.File.Save(j); err != nil {
		return err
	}
	return printCluster(cmd.OutOrStdout(), actual, o.outputFmt)
}
func validateSubmission(j *application.Journal, actual, want *v1.Cluster) error {
	if actual == nil || actual.UID == "" || actual.Name != j.Cluster.Name || actual.Namespace != j.Cluster.Namespace || actual.Spec.InfraID != want.Spec.InfraID || actual.Spec.IssuerURL != want.Spec.IssuerURL || actual.Spec.Release != want.Spec.Release || !apiequality.Semantic.DeepEqual(actual.Spec.Platform.GCP, want.Spec.Platform.GCP) || !apiequality.Semantic.DeepEqual(actual.Spec.Maintenance, want.Spec.Maintenance) || (j.Cluster.UID != "" && j.Cluster.UID != string(actual.UID)) {
		return fmt.Errorf("Cluster result does not match the exact installation identity; outcome remains journaled")
	}
	if !j.Cluster.PreflightAbsent || actual.CreationTimestamp.IsZero() {
		return fmt.Errorf("submission lacks an established fresh-name preflight or server identity")
	}
	return nil
}

func definitiveAPI(err error) bool {
	var httpError *platformapi.HTTPError
	if errors.As(err, &httpError) {
		switch httpError.StatusCode() {
		case 400, 401, 403, 404, 422, 429:
			return true
		}
	}
	return apierrors.IsInvalid(err) || apierrors.IsForbidden(err) || apierrors.IsBadRequest(err) || apierrors.IsUnauthorized(err)
}
func apiNotFound(err error) bool {
	return platformapi.IsResourceNotFound(err)
}
func ensureNewInstallation(ctx context.Context, c *platformapi.Client, name, infra string) error {
	if _, err := c.Clusters().Get(ctx, c.Namespace(), name); !apiNotFound(err) {
		if err != nil {
			return err
		}
		return fmt.Errorf("Cluster name already exists; refusing creation")
	}
	list, err := c.Clusters().List(ctx, c.Namespace())
	if err != nil {
		return err
	}
	if list.Continue != "" {
		return fmt.Errorf("complete namespace inventory is required before provisioning")
	}
	for _, cluster := range list.Items {
		if cluster.Name == name || cluster.Spec.InfraID == infra {
			return fmt.Errorf("Cluster name or installation identity already exists")
		}
	}
	return nil
}
