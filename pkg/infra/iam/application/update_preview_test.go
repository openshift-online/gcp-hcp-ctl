package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type updateFixture struct {
	client              *platformapi.Client
	cloud               *fakeCloud
	base                *Journal
	cluster             *v1.Cluster
	pools               *v1.NodePoolList
	version             *v1.Version
	old                 *v1.Version
	target              *v1.WIFConfigTemplate
	history             *v1.WIFConfigTemplate
	writes              int
	clusterReads        int
	mutateSecondCluster bool
	poolReads           int
	mutateSecondPool    bool
	targetReads         int
	mutateSecondTarget  bool
	patchFailure        int
	patchLost           bool
	archivedTemplates   map[string]*v1.WIFConfigTemplate
}

func newUpdateFixture(t *testing.T) *updateFixture {
	t.Helper()
	f := &updateFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			f.writes++
			w.Header().Set("Content-Type", "application/json")
			if r.Method != http.MethodPatch || f.patchFailure != 0 {
				code := f.patchFailure
				if code == 0 {
					code = http.StatusMethodNotAllowed
				}
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden"}`))
				return
			}
			var patch []struct {
				Op, Path string
				Value    json.RawMessage
			}
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Errorf("decode assertion patch: %v", err)
				w.WriteHeader(400)
				return
			}
			if r.Header.Get("Content-Type") != "application/json-patch+json" || len(patch) != 3 || patch[0].Op != "test" || patch[0].Path != "/metadata/uid" || patch[1].Op != "test" || patch[1].Path != "/metadata/resourceVersion" || patch[2].Op != "replace" || patch[2].Path != "/spec/platform/gcp/iam/templateApplications" {
				t.Error("assertion patch did not restrict field and test UID/RV")
				w.WriteHeader(400)
				return
			}
			var uid, rv string
			_ = json.Unmarshal(patch[0].Value, &uid)
			_ = json.Unmarshal(patch[1].Value, &rv)
			if rv != f.cluster.ResourceVersion || uid != string(f.cluster.UID) {
				w.WriteHeader(409)
				return
			}
			if err := json.Unmarshal(patch[2].Value, &f.cluster.Spec.Platform.GCP.IAM.TemplateApplications); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			f.cluster.Generation++
			f.cluster.ResourceVersion = "after-assertion"
			f.cluster.Status.IAMRequirements.ObservedGeneration = f.cluster.Generation
			if f.patchLost {
				f.patchLost = false
				w.WriteHeader(503)
				return
			}
			_ = json.NewEncoder(w).Encode(f.cluster)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if item := f.archivedTemplates[r.URL.Path]; item != nil {
			_ = json.NewEncoder(w).Encode(item)
			return
		}
		var obj any
		switch r.URL.Path {
		case "/apis/gcp.managed.openshift.io/v1/namespaces/test-project/clusters/owned":
			f.clusterReads++
			if f.mutateSecondCluster && f.clusterReads == 2 {
				f.cluster.ResourceVersion = "12"
				f.cluster.Generation++
			}
			obj = f.cluster
		case "/apis/gcp.managed.openshift.io/v1/namespaces/test-project/nodepools":
			f.poolReads++
			if f.mutateSecondPool && f.poolReads == 2 {
				f.pools.Items[0].ResourceVersion = "6"
				f.pools.Items[0].Generation++
			}
			obj = f.pools
		case "/apis/gcp.managed.openshift.io/v1/versions/" + f.old.Name:
			obj = f.old
		case "/apis/gcp.managed.openshift.io/v1/versions/" + f.version.Name:
			f.targetReads++
			if f.mutateSecondTarget && f.targetReads == 2 {
				f.version.Status.Conditions[0].Status = metav1.ConditionFalse
			}
			obj = f.version
		case "/apis/gcp.managed.openshift.io/v1/wifconfigtemplates/" + f.history.Name:
			obj = f.history
		case "/apis/gcp.managed.openshift.io/v1/wifconfigtemplates/" + f.target.Name:
			obj = f.target
		default:
			t.Errorf("unexpected catalog request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewEncoder(w).Encode(obj); err != nil {
			t.Errorf("encode catalog response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	var err error
	f.client, err = platformapi.NewClientForTest(server.URL, "test-project")
	if err != nil {
		t.Fatal(err)
	}
	base := testTarget(t)
	id := testPlan(t).Identity
	id.APIEndpoint = server.URL
	p, err := Build(base, id)
	if err != nil {
		t.Fatal(err)
	}
	f.base, err = newJournal(p)
	if err != nil {
		t.Fatal(err)
	}
	f.cloud = fakeBackend(p)
	engine := &Engine{Cloud: f.cloud, Store: &memoryJournal{}, Recheck: func(ctx context.Context) error { return nil }, Pause: func(ctx context.Context) error { return nil }}
	if err := engine.Apply(t.Context(), f.base); err != nil {
		t.Fatal(err)
	}
	f.base.Phase = "ClusterSubmitted"
	f.base.Cluster = Submission{State: "Confirmed", Name: "owned", Namespace: "test-project", UID: "cluster-uid"}
	now := metav1.NewTime(time.Now().UTC().Add(-time.Minute))
	baseRequirement := &v1.IAMReleaseRequirement{ReleaseVersion: p.Target.Version, ReleaseImage: "quay.io/example/release@sha256:" + strings.Repeat("a", 64), ChannelGroup: "candidate", TemplateRef: p.Target.Reference}
	f.cluster = &v1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "owned", Namespace: "test-project", UID: "cluster-uid", ResourceVersion: "11", Generation: 2},
		Spec: v1.ClusterSpec{InfraID: id.InfraID, IssuerURL: id.Issuer, Release: v1.ClusterReleaseSpec{Version: p.Target.Version, ChannelGroup: "candidate"},
			Platform: v1.ClusterPlatformSpec{Type: "GCP", GCP: &v1.GCPClusterPlatform{ProjectID: id.Project, Region: "us-central1", Network: "owned-network", Subnet: "owned-subnet", WorkloadIdentity: v1.WorkloadIdentitySpec{PoolID: id.InfraID + "-wi-pool", ProjectNumber: id.ProjectNumber, ProviderID: id.InfraID + "-k8s-provider"}, IAM: &v1.ClusterIAMSpec{WorkerServiceAccount: id.WorkerEmail, TemplateApplications: []v1.WIFConfigTemplateReference{p.Target.Reference}}}}},
		Status: v1.ClusterStatus{IAMRequirements: &v1.IAMRequirementsStatus{State: "Complete", ObjectUID: "cluster-uid", ClusterUID: "cluster-uid", ObservedGeneration: 2, LastRefreshTime: &now, Dispatched: baseRequirement}}}
	f.base.Cluster.Payload, err = json.Marshal(f.cluster)
	if err != nil {
		t.Fatal(err)
	}
	f.pools = &v1.NodePoolList{ListMeta: metav1.ListMeta{ResourceVersion: "20"}, Items: []v1.NodePool{{ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "test-project", UID: "pool-uid", ResourceVersion: "5", Generation: 1}, Spec: v1.NodePoolSpec{ClusterID: "owned"}, Status: v1.NodePoolStatus{IAMRequirements: &v1.IAMRequirementsStatus{State: "Complete", ObjectUID: "pool-uid", ClusterUID: "cluster-uid", ObservedGeneration: 1, LastRefreshTime: &now, Dispatched: baseRequirement}}}}}
	f.old = &v1.Version{ObjectMeta: metav1.ObjectMeta{Name: p.Target.Version, UID: "version-uid", Generation: 2}, Spec: v1.VersionSpec{ChannelGroups: []string{"candidate"}, WIFConfigTemplateRef: &p.Target.Reference}, Status: v1.VersionStatus{Conditions: []metav1.Condition{{Type: "Available", Status: metav1.ConditionTrue, ObservedGeneration: 2}}}}
	f.history = &v1.WIFConfigTemplate{ObjectMeta: metav1.ObjectMeta{Name: p.Target.Reference.Name, UID: "template-uid"}, Spec: p.Target.Template}
	other := testUpdatePlan(t, p, nil)
	f.version = &v1.Version{ObjectMeta: metav1.ObjectMeta{Name: other.Target.Version, UID: "version-new-uid", Generation: 2}, Spec: v1.VersionSpec{ChannelGroups: []string{"candidate"}, WIFConfigTemplateRef: &other.Target.Reference}, Status: v1.VersionStatus{Conditions: []metav1.Condition{{Type: "Available", Status: metav1.ConditionTrue, ObservedGeneration: 2}}}}
	f.target = &v1.WIFConfigTemplate{ObjectMeta: metav1.ObjectMeta{Name: other.Target.Reference.Name, UID: "new-template-uid"}, Spec: other.Target.Template}
	return f
}

func TestUpdatePreviewPreservesRunningIAMAndMakesNoWrites(t *testing.T) {
	f := newUpdateFixture(t)
	before := f.cloud.writes
	var fingerprint string
	for i := 0; i < 2; i++ {
		got, err := PreviewUpdate(t.Context(), f.client, f.cloud, f.base, "owned", f.version.Name, "candidate", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if !got.TargetEligible || !got.NewApplication || got.ClusterUID != "cluster-uid" || got.ClusterResourceVersion != "11" || got.NodePoolListVersion != "20" || len(got.NodePools) != 1 || len(got.Retention.Required) != 1 || len(got.RoleAdditions) != 1 || !slices.Contains(got.RoleAdditions[0].Permissions, "compute.instances.start") {
			t.Fatalf("unsafe or incomplete preview: %+v", got)
		}
		if i == 0 {
			fingerprint = got.PlanFingerprint
		} else if got.PlanFingerprint != fingerprint {
			t.Fatal("unchanged preview changed fingerprint")
		}
	}
	if f.cloud.writes != before || f.writes != 0 {
		t.Fatal("preview mutated customer IAM or Platform API")
	}
}

func TestUpdatePreviewBlocksIncompleteInventoryAndUnownedCloud(t *testing.T) {
	for name, change := range map[string]func(*updateFixture){
		"wrong cluster UID":    func(f *updateFixture) { f.cluster.UID = "replacement" },
		"missing requirements": func(f *updateFixture) { f.cluster.Status.IAMRequirements = nil },
		"stale requirements": func(f *updateFixture) {
			t := metav1.NewTime(time.Now().Add(-time.Hour))
			f.cluster.Status.IAMRequirements.LastRefreshTime = &t
		},
		"future requirements": func(f *updateFixture) {
			t := metav1.NewTime(time.Now().Add(time.Minute))
			f.cluster.Status.IAMRequirements.LastRefreshTime = &t
		},
		"unknown worker":    func(f *updateFixture) { f.pools.Items[0].Status.IAMRequirements.State = "Incomplete" },
		"foreign pool":      func(f *updateFixture) { f.pools.Items[0].Status.IAMRequirements.ClusterUID = "other" },
		"paginated pools":   func(f *updateFixture) { f.pools.Continue = "more" },
		"unbound pool list": func(f *updateFixture) { f.pools.ResourceVersion = "" },
		"unrecorded pending": func(f *updateFixture) {
			f.pools.Items[0].Status.IAMRequirements.Pending = &v1.IAMReleaseRequirement{ReleaseVersion: "5.0.1", ReleaseImage: "image", ChannelGroup: "candidate", TemplateRef: *f.version.Spec.WIFConfigTemplateRef}
		},
		"role changed": func(f *updateFixture) {
			r := f.base.Plan.Resources[0]
			got := f.cloud.resources[resourceKey(r)]
			got.Permissions = []string{"compute.instances.delete"}
			f.cloud.resources[resourceKey(r)] = got
		},
		"worker SA UID changed in cloud": func(f *updateFixture) {
			worker := Resource{Kind: "ServiceAccount", Name: "projects/test-project/serviceAccounts/" + f.base.Plan.Identity.WorkerEmail}
			found := f.cloud.resources[resourceKey(worker)]
			found.UID = "replacement-worker"
			f.cloud.resources[resourceKey(worker)] = found
		},
		"historical content changed": func(f *updateFixture) {
			f.history.Spec.IAM.Roles[0].Permissions = append(f.history.Spec.IAM.Roles[0].Permissions, "compute.instances.start")
		},
		"concurrent Cluster update":  func(f *updateFixture) { f.mutateSecondCluster = true },
		"concurrent NodePool update": func(f *updateFixture) { f.mutateSecondPool = true },
		"target eligibility changed": func(f *updateFixture) { f.mutateSecondTarget = true },
		"duplicate pools":            func(f *updateFixture) { f.pools.Items = append(f.pools.Items, f.pools.Items[0]) },
		"deleting pool":              func(f *updateFixture) { n := metav1.Now(); f.pools.Items[0].DeletionTimestamp = &n },
		"missing history":            func(f *updateFixture) { f.cluster.Spec.Platform.GCP.IAM.TemplateApplications = nil },
		"wrong worker":               func(f *updateFixture) { f.cluster.Spec.Platform.GCP.IAM.WorkerServiceAccount = "foreign@example.com" },
		"wrong realm":                func(f *updateFixture) { f.old.UID = "replacement" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newUpdateFixture(t)
			change(f)
			writes := f.cloud.writes
			if _, err := PreviewUpdate(t.Context(), f.client, f.cloud, f.base, "owned", f.version.Name, "candidate", time.Now().UTC()); err == nil || f.writes != 0 || f.cloud.writes != writes {
				t.Fatalf("unsafe update preview accepted or wrote state: %v", err)
			}
		})
	}
}

type rejectNewPermissions struct {
	*fakeCloud
	max int
}

func (f rejectNewPermissions) CheckPermissions(_ context.Context, permissions []string) error {
	if len(permissions) > f.max {
		return errors.New("unsupported new permission")
	}
	return nil
}

func TestUpdatePreviewRejectsUnsupportedNewPermission(t *testing.T) {
	f := newUpdateFixture(t)
	var limit int
	for _, r := range f.base.Plan.Resources {
		limit += len(r.Permissions)
	}
	_, err := PreviewUpdate(t.Context(), f.client, rejectNewPermissions{fakeCloud: f.cloud, max: limit}, f.base, "owned", f.version.Name, "candidate", time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "target custom-role permissions are not supported") || f.writes != 0 {
		t.Fatalf("unsupported permission was accepted or wrote state: %v", err)
	}
}

func TestUpdatePreviewSameContractAndRevokedHistory(t *testing.T) {
	f := newUpdateFixture(t)
	f.version = f.old
	f.target = f.history
	f.history.Spec.Verified = new(false)
	got, err := PreviewUpdate(t.Context(), f.client, f.cloud, f.base, "owned", f.old.Name, "candidate", time.Now().UTC())
	if err != nil || got.NewApplication || len(got.RoleAdditions) != 0 || got.TargetEligible || got.TargetBlock != "WIFConfigTemplateUnverified" {
		t.Fatalf("same-contract revoked history preview = %+v, %v", got, err)
	}
	if f.writes != 0 {
		t.Fatal("revoked-history preview wrote API data")
	}
}

func TestUpdatePreviewRejectsUnconfirmedInstallation(t *testing.T) {
	for name, change := range map[string]func(*updateFixture){
		"phase":        func(f *updateFixture) { f.base.Phase = "Provisioning" },
		"submission":   func(f *updateFixture) { f.base.Cluster.State = "Unknown" },
		"verification": func(f *updateFixture) { f.base.VerifiedAt = nil },
		"name":         func(f *updateFixture) { f.base.Cluster.Name = "different" },
		"project":      func(f *updateFixture) { f.base.Plan.Identity.Project = "different" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newUpdateFixture(t)
			change(f)
			if _, err := PreviewUpdate(t.Context(), f.client, f.cloud, f.base, "owned", f.version.Name, "candidate", time.Now().UTC()); err == nil || f.clusterReads != 0 || f.writes != 0 {
				t.Fatalf("unconfirmed journal reached customer API: %v", err)
			}
		})
	}
}

func TestUpdatePreviewIgnoresOtherClustersPoolsAndRetainsAllHistory(t *testing.T) {
	f := newUpdateFixture(t)
	other := f.pools.Items[0].DeepCopy()
	other.Name = "foreign"
	other.Spec.ClusterID = "elsewhere"
	f.pools.Items = append(f.pools.Items, *other)
	f.cluster.Spec.Platform.GCP.IAM.TemplateApplications = append(f.cluster.Spec.Platform.GCP.IAM.TemplateApplications, *f.version.Spec.WIFConfigTemplateRef)
	got, err := PreviewUpdate(t.Context(), f.client, f.cloud, f.base, "owned", f.version.Name, "candidate", time.Now().UTC())
	if err != nil || len(got.NodePools) != 1 || len(got.Retention.Required) != 2 || got.NewApplication {
		t.Fatalf("incomplete historical or foreign-pool preview: %+v, %v", got, err)
	}
}
