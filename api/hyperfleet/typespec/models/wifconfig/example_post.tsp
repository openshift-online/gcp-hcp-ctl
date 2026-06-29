import "./model.tsp";

const exampleWifConfigCreateRequest: WifConfigCreateRequest = #{
  kind: "WifConfig",
  name: "my-wif-config",
  labels: #{ environment: "production" },
  spec: #{
    version: "4.17",
    project_id: "my-gcp-project-123",
  },
};
