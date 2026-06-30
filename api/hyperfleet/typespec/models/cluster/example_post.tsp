import "./model.tsp";
import "hyperfleet/shared/models/clusters/model.tsp";

const exampleClusterCreateRequest: ClusterCreateRequest = #{
  kind: "Cluster",
  name: "my-gcp-cluster",
  labels: #{ env: "dev", shard: "1" },
  spec: #{
    infraID: "my-gcp-cluster",
    issuerURL: "https://oidc.dev-reg-us-c1-abc123.dev.gcp-hcp.devshift.net/my-gcp-cluster",
    platform: #{
      type: "gcp",
      gcp: #{
        projectID: "my-gcp-project-123",
        region: "us-central1",
        network: "dev-mgt-us-c1-vpc",
        subnet: "dev-mgt-us-c1-psc-001",
        endpointAccess: "PublicAndPrivate",
        workloadIdentity: #{
          projectNumber: "123456789012",
          poolID: "my-gcp-cluster-pool",
          providerID: "my-gcp-cluster-provider",
          serviceAccountsRef: #{
            controlPlaneEmail: "my-gcp-cluster-ctrlplane@my-gcp-project-123.iam.gserviceaccount.com",
            nodePoolEmail: "my-gcp-cluster-nodepool@my-gcp-project-123.iam.gserviceaccount.com",
            cloudControllerEmail: "my-gcp-cluster-cloud-ctrl@my-gcp-project-123.iam.gserviceaccount.com",
            storageEmail: "my-gcp-cluster-gcp-pd-csi@my-gcp-project-123.iam.gserviceaccount.com",
            imageRegistryEmail: "my-gcp-cluster-img-reg@my-gcp-project-123.iam.gserviceaccount.com",
            networkEmail: "my-gcp-cluster-cloud-net@my-gcp-project-123.iam.gserviceaccount.com",
          },
        },
      },
    },
    release: #{
      version: "4.18.0",
      channelGroup: "candidate",
    },
  }
};
