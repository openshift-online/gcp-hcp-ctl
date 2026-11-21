package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/discovery"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/network"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/spf13/cobra"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type provisionCloud struct {
	resources map[string]application.Resource
	policies  map[string]*crm.Policy
	writes    int
	mismatch  bool
}

func (f *provisionCloud) ProjectNumber(context.Context) (string, error) { return "123456789", nil }
func (f *provisionCloud) Get(_ context.Context, r application.Resource) (*application.Resource, error) {
	if v, ok := f.resources[r.Name]; ok {
		if f.mismatch && v.Kind == "Role" {
			v.Permissions = []string{"compute.instances.delete"}
		}
		return &v, nil
	}
	return nil, &googleapi.Error{Code: 404}
}
func (f *provisionCloud) Create(_ context.Context, r application.Resource) (*application.Resource, error) {
	f.writes++
	r.UID = "created-" + r.Name
	f.resources[r.Name] = r
	f.policies[r.Name] = &crm.Policy{Etag: "etag", Version: 3}
	return &r, nil
}
func (f *provisionCloud) Delete(context.Context, application.Resource) error {
	return fmt.Errorf("unexpected cleanup")
}
func (f *provisionCloud) GetPolicy(_ context.Context, scope string) (*crm.Policy, error) {
	p := f.policies[scope]
	if p == nil {
		return nil, &googleapi.Error{Code: 404}
	}
	b, _ := json.Marshal(p)
	var out crm.Policy
	_ = json.Unmarshal(b, &out)
	return &out, nil
}
func (f *provisionCloud) SetPolicy(_ context.Context, scope string, p *crm.Policy) error {
	f.writes++
	f.policies[scope] = p
	return nil
}
func (f *provisionCloud) CheckPermissions(context.Context, []string) error { return nil }

type provisionNetwork struct {
	afterWait   func(string)
	resources   map[string]*network.Observed
	writes      int
	waits       int
	rejectCode  int
	expiredWait bool
}

func (f *provisionNetwork) Read(_ context.Context, _ network.OwnedPlan, kind, _ string) (*network.Observed, error) {
	v, ok := f.resources[kind]
	if !ok {
		return nil, &googleapi.Error{Code: 404}
	}
	copy := *v
	return &copy, nil
}
func (f *provisionNetwork) Start(_ context.Context, _ network.OwnedPlan, kind, owner string) (string, error) {
	f.writes++
	if f.rejectCode != 0 {
		code := f.rejectCode
		f.rejectCode = 0
		return "", &googleapi.Error{Code: code}
	}
	f.resources[kind] = &network.Observed{ID: fmt.Sprint(f.writes), Owner: owner, Matches: true}
	return "op-" + kind, nil
}
func (f *provisionNetwork) Wait(_ context.Context, kind, _ string) error {
	f.waits++
	if f.expiredWait {
		return fmt.Errorf("old operation expired")
	}
	if f.afterWait != nil {
		f.afterWait(kind)
	}
	return nil
}

func TestPostNetworkIAMDriftBlocksInitialAssertion(t *testing.T) {
	h := newProvisionHarness(t)
	defer h.close()
	h.verified.Store(true)
	o := h.options()
	o.expectPlan = h.preview(t, o)
	o.stateFile = privateStatePath(t)
	o.confirm = true
	o.dryRun = false
	h.network.afterWait = func(kind string) {
		if kind == "Router" {
			h.cloud.mismatch = true
		}
	}
	if err := o.run(h.cmd, "test-cluster"); err == nil {
		t.Fatal("post-network IAM drift was ignored")
	}
	if h.posts.Load() != 0 {
		t.Fatal("stale verification released an initial application assertion")
	}
}
func (f *provisionNetwork) TargetID(_ context.Context, _ network.OwnedPlan, kind, _ string) (string, error) {
	return f.resources[kind].ID, nil
}

type provisionHarness struct {
	cmd            *cobra.Command
	out            *bytes.Buffer
	cloud          *provisionCloud
	network        *provisionNetwork
	verified       atomic.Bool
	posts          atomic.Int32
	rejectPost     atomic.Int32
	loseResponse   atomic.Bool
	hideGet        atomic.Bool
	stateFile      atomic.Value
	clockSkew      atomic.Int64
	clusterHandler atomic.Value
	mu             sync.Mutex
	created        *v1.Cluster
	close          func()
}

func newProvisionHarness(t *testing.T) *provisionHarness {
	t.Helper()
	data, err := os.ReadFile("../infra/iam/application/testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec v1.WIFConfigTemplateSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	digest, err := application.Digest(spec)
	if err != nil {
		t.Fatal(err)
	}
	h := &provisionHarness{out: &bytes.Buffer{}, cloud: &provisionCloud{resources: map[string]application.Resource{}, policies: map[string]*crm.Policy{}}, network: &provisionNetwork{resources: map[string]*network.Observed{}}}
	worker := "projects/test-project/serviceAccounts/123456789-compute@developer.gserviceaccount.com"
	h.cloud.resources[worker] = application.Resource{Kind: "ServiceAccount", Name: worker, UID: "worker-uid"}
	h.cloud.policies[worker] = &crm.Policy{Etag: "etag", Version: 3}
	h.cloud.policies["project"] = &crm.Policy{Etag: "etag", Version: 3}
	ref := v1.WIFConfigTemplateReference{Name: "5.0.0", UID: "template-uid", Digest: digest}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if handler := h.clusterHandler.Load(); handler != nil && strings.Contains(r.URL.Path, "/clusters") {
			if r.Method == http.MethodPost {
				h.posts.Add(1)
				if path := h.stateFile.Load(); path != nil {
					j, err := application.Load(path.(string))
					if err != nil || j.Cluster.State != "Intent" || !j.Cluster.PreflightAbsent || j.VerifiedAt == nil {
						t.Error("real API POST preceded durable verified intent")
					}
				}
			}
			handler.(http.Handler).ServeHTTP(w, r)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/apis/gcp.managed.openshift.io/v1/namespaces/test-project/clusters" {
			_ = json.NewEncoder(w).Encode(&v1.ClusterList{})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/apis/gcp.managed.openshift.io/v1/versions/5.0.0" {
			status := metav1.ConditionFalse
			if h.verified.Load() {
				status = metav1.ConditionTrue
			}
			_ = json.NewEncoder(w).Encode(&v1.Version{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0", UID: "version-uid", Generation: 2}, Spec: v1.VersionSpec{ChannelGroups: []string{"stable"}, WIFConfigTemplateRef: &ref}, Status: v1.VersionStatus{Conditions: []metav1.Condition{{Type: "Available", Status: status, ObservedGeneration: 2}}}})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/apis/gcp.managed.openshift.io/v1/wifconfigtemplates/5.0.0" {
			copy := *spec.DeepCopy()
			value := h.verified.Load()
			copy.Verified = &value
			_ = json.NewEncoder(w).Encode(&v1.WIFConfigTemplate{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0", UID: "template-uid"}, Spec: copy})
			return
		}
		if r.Method == "POST" {
			h.posts.Add(1)
			if path := h.stateFile.Load(); path != nil {
				j, err := application.Load(path.(string))
				if err != nil || j.Cluster.State != "Intent" || !j.Cluster.PreflightAbsent || j.VerifiedAt == nil {
					t.Error("POST preceded durable verified-IAM/Cluster intent")
				}
			}
			if code := h.rejectPost.Load(); code != 0 {
				w.WriteHeader(int(code))
				_ = json.NewEncoder(w).Encode(&metav1.Status{Status: metav1.StatusFailure, Code: code, Reason: metav1.StatusReasonForbidden})
				return
			}
			var c v1.Cluster
			if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if c.Spec.Platform.GCP == nil || c.Spec.Platform.GCP.IAM == nil || len(c.Spec.Platform.GCP.IAM.TemplateApplications) != 1 || c.Spec.Platform.GCP.IAM.TemplateApplications[0] != ref {
				t.Error("POST missing exact verified application")
			}
			c.Namespace = "test-project"
			c.UID = "cluster-uid"
			c.CreationTimestamp = metav1.NewTime(time.Now().UTC().Add(time.Duration(h.clockSkew.Load())))
			h.mu.Lock()
			h.created = &c
			h.mu.Unlock()
			w.WriteHeader(201)
			if h.loseResponse.Load() {
				return
			}
			_ = json.NewEncoder(w).Encode(c)
			return
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if r.Method == "GET" && h.created != nil && !h.hideGet.Load() {
			_ = json.NewEncoder(w).Encode(h.created)
			return
		}
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(&metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: metav1.StatusFailure, Reason: metav1.StatusReasonNotFound, Code: 404})
	}))
	h.close = server.Close
	client, err := platformapi.NewClientForTest(server.URL, "test-project")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), clientKey, client)
	ctx = context.WithValue(ctx, regionInfoKey, discovery.RegionInfo{OIDCIssuer: "https://issuer.example"})
	h.cmd = &cobra.Command{}
	h.cmd.SetContext(ctx)
	h.cmd.SetOut(h.out)
	h.cmd.Flags().String("region", "us-central1", "")
	return h
}

func TestDryRunSubmittedResumeNeverWritesJournal(t *testing.T) {
	h := newProvisionHarness(t)
	defer h.close()
	h.verified.Store(true)
	o := h.options()
	o.expectPlan = h.preview(t, o)
	o.stateFile = privateStatePath(t)
	o.confirm = true
	o.dryRun = false
	h.stateFile.Store(o.stateFile)
	if err := o.run(h.cmd, "test-cluster"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(o.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(o.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	o.dryRun = true
	o.resume = true
	h.verified.Store(false)
	if err := o.run(h.cmd, "test-cluster"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(o.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	newInfo, err := os.Stat(o.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !info.ModTime().Equal(newInfo.ModTime()) || h.posts.Load() != 1 {
		t.Fatal("resume preview wrote state or repeated POST")
	}
}

func TestDefinitivePOSTFailureResumesWithoutExpiredOperationWait(t *testing.T) {
	for _, code := range []int32{400, 403, 422, 429} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			h := newProvisionHarness(t)
			defer h.close()
			h.verified.Store(true)
			o := h.options()
			o.expectPlan = h.preview(t, o)
			o.stateFile = privateStatePath(t)
			o.dryRun = false
			o.confirm = true
			h.stateFile.Store(o.stateFile)
			h.rejectPost.Store(code)
			if err := o.run(h.cmd, "test-cluster"); err == nil {
				t.Fatal("rejected POST succeeded")
			}
			j, err := application.Load(o.stateFile)
			if err != nil {
				t.Fatal(err)
			}
			if j.Cluster.State != "None" {
				t.Fatal("definitive API rejection wedged submission")
			}
			writes := h.cloud.writes
			nWrites := h.network.writes
			waits := h.network.waits
			h.rejectPost.Store(0)
			h.network.expiredWait = true
			o.resume = true
			if err := o.run(h.cmd, "test-cluster"); err != nil {
				t.Fatal(err)
			}
			if h.cloud.writes != writes || h.network.writes != nWrites || h.network.waits != waits {
				t.Fatal("resume repeated verified cloud work or an expired Wait")
			}
		})
	}
}

func TestUnknownPOSTResolvesExactIdentityWithoutSecondPOST(t *testing.T) {
	h := newProvisionHarness(t)
	defer h.close()
	h.verified.Store(true)
	o := h.options()
	o.expectPlan = h.preview(t, o)
	o.stateFile = privateStatePath(t)
	o.dryRun = false
	o.confirm = true
	h.stateFile.Store(o.stateFile)
	h.loseResponse.Store(true)
	h.clockSkew.Store(int64(-15 * time.Minute))
	if err := o.run(h.cmd, "test-cluster"); err != nil {
		t.Fatal(err)
	}
	j, err := application.Load(o.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if j.Cluster.State != "Confirmed" || h.posts.Load() != 1 {
		t.Fatal("uncertain response was recreated rather than resolved")
	}
}

func TestMaintenanceResumeUsesOriginalStartAcrossMidnight(t *testing.T) {
	h := newProvisionHarness(t)
	defer h.close()
	h.verified.Store(true)
	o := h.options()
	start := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	o.now = func() time.Time { return start }
	o.maintenanceUntil = start.Add(3 * 24 * time.Hour).Format(time.RFC3339)
	o.expectPlan = h.preview(t, o)
	o.stateFile = privateStatePath(t)
	o.dryRun = false
	o.confirm = true
	h.stateFile.Store(o.stateFile)
	h.network.rejectCode = 403
	if err := o.run(h.cmd, "test-cluster"); err == nil {
		t.Fatal("expected network rejection")
	}
	j, err := application.Load(o.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	var prior v1.Cluster
	if err := json.Unmarshal(j.InstallationPayload, &prior); err != nil {
		t.Fatal(err)
	}
	o.now = func() time.Time { return start.Add(24 * time.Hour) }
	o.resume = true
	if err := o.run(h.cmd, "test-cluster"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !reflect.DeepEqual(h.created.Spec.Maintenance, prior.Spec.Maintenance) {
		t.Fatal("resume moved the reviewed maintenance start")
	}
}
func (h *provisionHarness) options() *createOptions {
	return &createOptions{infraID: "fresh-123", setupInfra: true, version: "5.0.0", channelGroup: "stable", endpointAccess: "PublicAndPrivate", dryRun: true, iamCloud: h.cloud, networkCloud: h.network, outputFmt: "json"}
}
func (h *provisionHarness) preview(t *testing.T, o *createOptions) string {
	t.Helper()
	h.out.Reset()
	if err := o.run(h.cmd, "test-cluster"); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Fingerprint string `json:"planFingerprint"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Fingerprint == "" {
		t.Fatal("preview omitted stable fingerprint")
	}
	return result.Fingerprint
}
func privateStatePath(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "operation.json")
}

func TestSetupInfraPreviewBlockedWritesNothing(t *testing.T) {
	h := newProvisionHarness(t)
	defer h.close()
	o := h.options()
	o.stateFile = privateStatePath(t)
	h.preview(t, o)
	if h.cloud.writes != 0 || h.network.writes != 0 || h.posts.Load() != 0 {
		t.Fatal("preview mutated IAM, network or platform")
	}
	if _, err := os.Stat(o.stateFile); !os.IsNotExist(err) {
		t.Fatal("preview created an operation file")
	}
	o.dryRun = false
	o.confirm = true
	o.expectPlan = "wrong"
	if err := o.run(h.cmd, "test-cluster"); err == nil || h.cloud.writes != 0 {
		t.Fatal("blocked or mismatching plan was applied")
	}
}
func TestSetupInfraVerifiedPOSTAndRevokedResume(t *testing.T) {
	h := newProvisionHarness(t)
	defer h.close()
	h.verified.Store(true)
	o := h.options()
	o.expectPlan = h.preview(t, o)
	o.stateFile = privateStatePath(t)
	o.dryRun = false
	o.confirm = true
	if err := o.run(h.cmd, "test-cluster"); err != nil {
		t.Fatal(err)
	}
	if h.posts.Load() != 1 || h.network.writes != 3 {
		t.Fatalf("posts=%d network writes=%d", h.posts.Load(), h.network.writes)
	}
	j, err := application.Load(o.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if j.Cluster.State != "Confirmed" || j.Cluster.UID != "cluster-uid" || j.Phase != "ClusterSubmitted" {
		t.Fatal("POST outcome not journaled")
	}
	writes := h.cloud.writes
	networkWrites := h.network.writes
	o.resume = true
	h.verified.Store(false)
	if err := o.run(h.cmd, "test-cluster"); err != nil {
		t.Fatal(err)
	}
	if h.posts.Load() != 1 || h.cloud.writes != writes || h.network.writes != networkWrites {
		t.Fatal("known submission was recreated or mutated after revocation")
	}
}
func TestSetupInfraReadbackMismatchNeverPosts(t *testing.T) {
	h := newProvisionHarness(t)
	defer h.close()
	h.verified.Store(true)
	o := h.options()
	o.expectPlan = h.preview(t, o)
	o.stateFile = privateStatePath(t)
	o.dryRun = false
	o.confirm = true
	h.cloud.mismatch = true
	if err := o.run(h.cmd, "test-cluster"); err == nil {
		t.Fatal("cloud mismatch ignored")
	}
	if h.posts.Load() != 0 || h.network.writes != 0 {
		t.Fatal("partial IAM claimed coverage or continued network allocation")
	}
	j, err := application.Load(o.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if j.VerifiedAt != nil || j.Cluster.State != "None" {
		t.Fatal("failed verification created a success assertion")
	}
}
