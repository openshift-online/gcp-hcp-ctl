import "./model.tsp";
import "hyperfleet/shared/models/common/model.tsp";

const exampleWifConfig: WifConfig = #{
  kind: "WifConfig",
  id: "019466a2-5678-7abc-9def-0123456789ab",
  href: "/api/hyperfleet/v1/wifconfigs/019466a2-5678-7abc-9def-0123456789ab",
  name: "my-wif-config",
  labels: #{ environment: "production" },
  spec: #{
    version: "4.17",
    project_id: "my-gcp-project-123",
  },
  generation: 1,
  status: #{
    conditions: #[],
  },
  created_time: "2025-06-01T00:00:00Z",
  updated_time: "2025-06-01T10:02:00Z",
  created_by: "user-123@example.com",
  updated_by: "user-123@example.com",
};
