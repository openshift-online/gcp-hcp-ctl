package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openshift-online/gcp-hcp-ctl/pkg/platformapi"
	v1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTeardownGuardRequiresNormalAbsenceAndCompleteReferences(t *testing.T) {
	for _, scenario := range []string{"gone", "active", "forbidden", "unknown", "other-reference", "dependants", "partial-list", "wrong-endpoint", "wrong-version", "proxy404"} {
		t.Run(scenario, func(t *testing.T) {
			j, err := newJournal(testPlan(t))
			if err != nil {
				t.Fatal(err)
			}
			j.Cluster = Submission{State: "Confirmed", Namespace: "test-project", Name: "fresh-cluster", UID: "cluster-uid"}
			if scenario == "unknown" {
				j.Cluster.State = "Unknown"
			}
			var gets atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "/versions/") {
					v := &v1.Version{ObjectMeta: metav1.ObjectMeta{Name: j.Plan.Target.Version, UID: "version-uid"}}
					if scenario == "wrong-version" {
						v.UID = "replacement"
					}
					_ = json.NewEncoder(w).Encode(v)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/clusters/fresh-cluster") {
					gets.Add(1)
					switch scenario {
					case "active":
						_ = json.NewEncoder(w).Encode(&v1.Cluster{})
					case "forbidden":
						w.WriteHeader(403)
					default:
						w.WriteHeader(404)
						if scenario != "proxy404" {
							_ = json.NewEncoder(w).Encode(&metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: metav1.StatusFailure, Reason: metav1.StatusReasonNotFound, Code: 404})
						}
					}
					return
				}
				if strings.HasSuffix(r.URL.Path, "/nodepools") {
					_ = json.NewEncoder(w).Encode(&v1.NodePoolList{})
					return
				}
				list := &v1.ClusterList{}
				if scenario == "other-reference" {
					c := v1.Cluster{}
					c.Spec.InfraID = j.Plan.Identity.InfraID
					list.Items = []v1.Cluster{c}
				}
				if scenario == "partial-list" {
					list.Continue = "more"
				}
				_ = json.NewEncoder(w).Encode(list)
			}))
			defer server.Close()
			client, err := platformapi.NewClientForTest(server.URL, "test-project")
			if err != nil {
				t.Fatal(err)
			}
			j.Plan.Identity.APIEndpoint = server.URL
			if scenario == "wrong-endpoint" {
				j.Plan.Identity.APIEndpoint = "https://different-api.example"
			}
			waits := 0
			cloudReads := 0
			guard := TeardownGuard{Client: client, Wait: func(context.Context) error {
				waits++
				if gets.Load() != 1 {
					t.Fatal("absence interval was not between distinct reads")
				}
				return nil
			}, NoDependents: func(context.Context, *Plan) error {
				cloudReads++
				if scenario == "dependants" {
					return fmt.Errorf("cloud dependants remain")
				}
				return nil
			}}
			err = guard.Check(t.Context(), j)
			if scenario == "gone" {
				if err != nil || gets.Load() != 2 || waits != 1 || cloudReads != 1 {
					t.Fatalf("normal teardown refused: %v", err)
				}
			} else if err == nil {
				t.Fatal("unproven teardown accepted")
			}
			if scenario == "unknown" && (gets.Load() != 0 || cloudReads != 0) {
				t.Fatal("unknown POST outcome was inferred from absence polling")
			}
			if (scenario == "wrong-endpoint" || scenario == "wrong-version") && (gets.Load() != 0 || cloudReads != 0) {
				t.Fatal("wrong realm reached hosted absence/cloud proof")
			}
		})
	}
}
