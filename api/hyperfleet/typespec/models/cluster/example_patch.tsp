import "./model.tsp";
import "hyperfleet/shared/models/clusters/model.tsp";

const exampleClusterPatchRequest: ClusterPatchRequest = #{
  spec: #{
    platform: #{
      type: "gcp",
      gcp: #{
        projectID: "my-gcp-project-123",
        region: "us-central1",
        network: "dev-mgt-us-c1-vpc",
        subnet: "dev-mgt-us-c1-psc-001",
      },
    },
    release: #{
      version: "4.18.1",
      channelGroup: "stable",
    },
  },
  labels: #{ env: "staging" },
};
