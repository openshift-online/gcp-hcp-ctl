import "./model.tsp";
import "hyperfleet/shared/models/common/model.tsp";

const exampleVersion: Version = #{
  kind: "Version",
  id: "019466a3-5678-7abc-9def-0123456789ab",
  href: "/api/hyperfleet/v1/channels/019466a2-1234-7abc-9def-0123456789ab/versions/019466a3-5678-7abc-9def-0123456789ab",
  name: "4.17.12",
  labels: #{},
  spec: #{
    raw_version: "4.17.12",
    enabled: true,
    is_default: true,
    release_image: "quay.io/openshift-release-dev/ocp-release:4.17.12-multi",
  },
  generation: 1,
  owner_references: #{
    id: "019466a2-1234-7abc-9def-0123456789ab",
    kind: "Channel",
    href: "/api/hyperfleet/v1/channels/019466a2-1234-7abc-9def-0123456789ab",
  },
  status: #{
    conditions: #[],
  },
  created_time: "2025-06-01T00:00:00Z",
  updated_time: "2025-06-01T10:02:00Z",
  created_by: "user-123@example.com",
  updated_by: "user-123@example.com",
};
