import "./model.tsp";
import "hyperfleet/shared/models/nodepools/model.tsp";

const exampleNodePoolPatchRequest: NodePoolPatchRequest = #{
  spec: #{
    replicas: 4,
    platform: #{
      type: "gcp",
      gcp: #{
        instanceType: "n2-standard-8",
        rootVolume: #{
          size: 200,
          type: "pd-ssd",
        },
      },
    },
    release: #{
      version: "4.18.1",
      channelGroup: "stable",
    },
  },
  labels: #{ env: "staging" },
};
