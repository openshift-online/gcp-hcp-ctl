import "./model.tsp";
import "hyperfleet/shared/models/nodepools/model.tsp";

const exampleNodePoolCreateRequest: NodePoolCreateRequest = #{
  kind: "NodePool",
  name: "worker-pool-1",
  labels: #{ environment: "production", pooltype: "worker" },
  spec: #{
    replicas: 2,
    platform: #{
      type: "gcp",
      gcp: #{
        instanceType: "n2-standard-4",
        rootVolume: #{
          size: 100,
          type: "pd-balanced",
        },
        zone: "us-central1-a",
      },
    },
    release: #{
      version: "4.18.0",
      channelGroup: "candidate",
    },
  },
};
