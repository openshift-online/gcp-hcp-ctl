import "./model.tsp";
import "hyperfleet/shared/models/nodepools/model.tsp";
import "hyperfleet/shared/models/common/model.tsp";

const exampleNodePool: NodePool = #{
  kind: "NodePool",
  id: "019466a1-2b3c-7def-8abc-456789abcdef",
  href: "https://api.hyperfleet.com/v1/nodepools/019466a1-2b3c-7def-8abc-456789abcdef",
  name: "worker-pool-1",
  labels: #{ environment: "production", pooltype: "worker" },
  generation: 1,
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
  owner_references: #{
    id: "019466a0-8f8e-7abc-9def-0123456789ab",
    kind: "Cluster",
    href: "https://api.hyperfleet.com/v1/clusters/019466a0-8f8e-7abc-9def-0123456789ab",
  },
  status: #{
    conditions: #[
      #{
        type: ConditionType.Reconciled,
        status: ResourceConditionStatus.True,
        reason: ExampleReconciledReason,
        message: ExampleReconciledMessage,
        observed_generation: 1,
        created_time: "2021-01-01T10:00:00Z",
        last_updated_time: "2021-01-01T10:00:00Z",
        last_transition_time: "2021-01-01T10:00:00Z",
      },
      #{
        type: ConditionType.LastKnownReconciled,
        status: ResourceConditionStatus.True,
        reason: ExampleLastKnownReconciledReason,
        message: ExampleLastKnownReconciledMessage,
        observed_generation: 1,
        created_time: "2021-01-01T10:00:00Z",
        last_updated_time: "2021-01-01T10:00:00Z",
        last_transition_time: "2021-01-01T10:00:00Z",
      },
      #{
        type: "VersionResolved",
        status: ResourceConditionStatus.True,
        reason: "ReleaseImageAvailable",
        message: "Version 4.18.0 resolved to release image via Cincinnati channel candidate-4.18",
        observed_generation: 1,
        created_time: "2021-01-01T10:00:00Z",
        last_updated_time: "2021-01-01T10:00:00Z",
        last_transition_time: "2021-01-01T10:00:00Z",
      },
      #{
        type: "NodePoolApplied",
        status: ResourceConditionStatus.True,
        reason: "ManifestWorkApplied",
        message: "NodePool ManifestWork applied to management cluster",
        observed_generation: 1,
        created_time: "2021-01-01T10:01:00Z",
        last_updated_time: "2021-01-01T10:01:00Z",
        last_transition_time: "2021-01-01T10:01:00Z",
      },
    ],
  },
  created_time: "2021-01-01T00:00:00Z",
  updated_time: "2021-01-01T10:02:00Z",
  created_by: "user@redhat.com",
  updated_by: "user@redhat.com",
};

const exampleDeletedNodePool: NodePool = #{
  kind: "NodePool",
  id: "019466a1-2b3c-7def-8abc-456789abcdef",
  href: "https://api.hyperfleet.com/v1/nodepools/019466a1-2b3c-7def-8abc-456789abcdef",
  name: "worker-pool-1",
  labels: #{ environment: "production", pooltype: "worker" },
  generation: 2,
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
  owner_references: #{
    id: "019466a0-8f8e-7abc-9def-0123456789ab",
    kind: "Cluster",
    href: "https://api.hyperfleet.com/v1/clusters/019466a0-8f8e-7abc-9def-0123456789ab",
  },
  status: #{
    conditions: #[
      #{
        type: ConditionType.Reconciled,
        status: ResourceConditionStatus.True,
        reason: ExampleReconciledReason,
        message: ExampleReconciledMessage,
        observed_generation: 2,
        created_time: "2021-01-01T10:00:00Z",
        last_updated_time: "2021-01-01T10:00:00Z",
        last_transition_time: "2021-01-01T10:00:00Z",
      },
      #{
        type: ConditionType.LastKnownReconciled,
        status: ResourceConditionStatus.True,
        reason: ExampleLastKnownReconciledReason,
        message: ExampleLastKnownReconciledMessage,
        observed_generation: 2,
        created_time: "2021-01-01T10:00:00Z",
        last_updated_time: "2021-01-01T10:00:00Z",
        last_transition_time: "2021-01-01T10:00:00Z",
      },
      #{
        type: "VersionResolved",
        status: ResourceConditionStatus.True,
        reason: "ReleaseImageAvailable",
        message: "Version 4.18.0 resolved to release image via Cincinnati channel candidate-4.18",
        observed_generation: 2,
        created_time: "2021-01-01T10:00:00Z",
        last_updated_time: "2021-01-01T10:00:00Z",
        last_transition_time: "2021-01-01T10:00:00Z",
      },
      #{
        type: "NodePoolApplied",
        status: ResourceConditionStatus.True,
        reason: "ManifestWorkApplied",
        message: "NodePool ManifestWork applied to management cluster",
        observed_generation: 2,
        created_time: "2021-01-01T10:01:00Z",
        last_updated_time: "2021-01-01T10:01:00Z",
        last_transition_time: "2021-01-01T10:01:00Z",
      },
    ],
  },
  created_time: "2021-01-01T00:00:00Z",
  updated_time: "2021-01-01T10:02:00Z",
  deleted_time: "2021-01-01T10:05:00Z",
  created_by: "user@redhat.com",
  updated_by: "user@redhat.com",
  deleted_by: "user@redhat.com",
};
