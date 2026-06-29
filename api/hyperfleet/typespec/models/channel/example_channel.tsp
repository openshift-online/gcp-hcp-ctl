import "./model.tsp";
import "hyperfleet/shared/models/common/model.tsp";

const exampleChannel: Channel = #{
  kind: "Channel",
  id: "019466a2-1234-7abc-9def-0123456789ab",
  href: "/api/hyperfleet/v1/channels/019466a2-1234-7abc-9def-0123456789ab",
  name: "stable",
  labels: #{ tier: "production" },
  spec: #{
    is_default: true,
    enabled_regex: "^4\\.\\d+\\.\\d+$",
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
