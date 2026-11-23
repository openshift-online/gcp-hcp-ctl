package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-logr/logr"
	"github.com/openshift-online/gcp-hcp-ctl/pkg/infra/iam/application"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/conversion"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/handlers"
	pkgschema "github.com/openshift-online/gecko/orlop/pkg/apiserver/schema"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/memory"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	publicv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

func TestProvisionPersistsOnlyRealAPIValidatedApplication(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			h := newProvisionHarness(t)
			defer h.close()
			h.verified.Store(true)
			privateScheme, publicScheme := runtime.NewScheme(), runtime.NewScheme()
			if err := privatev1.AddToScheme(privateScheme); err != nil {
				t.Fatal(err)
			}
			if err := publicv1.AddToScheme(publicScheme); err != nil {
				t.Fatal(err)
			}
			var schemaV1 apiextv1.JSONSchemaProps
			if err := yaml.Unmarshal([]byte(publicv1.ClusterSchemaYAML), &schemaV1); err != nil {
				t.Fatal(err)
			}
			var internal apiext.JSONSchemaProps
			if err := apiextv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&schemaV1, &internal, nil); err != nil {
				t.Fatal(err)
			}
			structural, err := structuralschema.NewStructural(&internal)
			if err != nil {
				t.Fatal(err)
			}
			processor, err := pkgschema.NewProcessor(structural, &internal)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile("../infra/iam/application/testdata/contract.json")
			if err != nil {
				t.Fatal(err)
			}
			template := &privatev1.WIFConfigTemplate{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0", UID: "template-uid"}}
			if err := json.Unmarshal(data, &template.Spec); err != nil {
				t.Fatal(err)
			}
			template.Spec.Verified = new(true)
			// This dependency injection affects only the local test handler/process,
			// never the separately running dev API or its rollout flags.
			privatev1.SetValidatorDeps(privatev1.ValidatorDeps{AllowIAMApplicationWrites: enabled, WIFConfigTemplate: func(context.Context, string) (*privatev1.WIFConfigTemplate, error) { return template.DeepCopy(), nil }})
			defer privatev1.SetValidatorDeps(privatev1.ValidatorDeps{})
			gvk := privatev1.GroupVersion.WithKind("Cluster")
			store := memory.NewMemoryStore("clusters", privateScheme, gvk)
			api := handlers.NewConvertingResourceHandler(store, processor, conversion.NewConverter(publicScheme, privateScheme, ""), gvk, "clusters", publicScheme, privateScheme, nil, logr.Discard())
			router := chi.NewRouter()
			base := "/apis/gcp.managed.openshift.io/v1/namespaces/{namespace}/clusters"
			router.Post(base, api.Create)
			router.Get(base, api.List)
			router.Get(base+"/{name}", api.Get)
			h.clusterHandler.Store(router)
			o := h.options()
			o.expectPlan = h.preview(t, o)
			o.stateFile = privateStatePath(t)
			h.stateFile.Store(o.stateFile)
			o.dryRun = false
			o.confirm = true
			err = o.run(h.cmd, "test-cluster")
			stored, lookupErr := store.Get(t.Context(), "test-project", "test-cluster")
			j, journalErr := application.Load(o.stateFile)
			if journalErr != nil {
				t.Fatal(journalErr)
			}
			if !enabled {
				if err == nil || !apierrors.IsNotFound(lookupErr) || j.Cluster.State != "None" {
					t.Fatalf("API rollout gate did not block publication: %v %v", err, lookupErr)
				}
				return
			}
			if err != nil || lookupErr != nil {
				t.Fatalf("verified application rejected: %v %v", err, lookupErr)
			}
			c := stored.(*privatev1.Cluster)
			if c.Spec.Platform.GCP.IAM == nil || c.Spec.Platform.GCP.IAM.WorkerServiceAccount != "123456789-compute@developer.gserviceaccount.com" || len(c.Spec.Platform.GCP.IAM.TemplateApplications) != 1 || c.Spec.Platform.GCP.IAM.TemplateApplications[0].Digest != j.Plan.Target.Reference.Digest || string(c.UID) != j.Cluster.UID || j.Cluster.State != "Confirmed" {
				t.Fatal("persisted API state does not match independent verification and journal")
			}
		})
	}
}
