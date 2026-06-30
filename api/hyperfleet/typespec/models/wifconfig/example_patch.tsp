import "./model.tsp";

const exampleWifConfigPatchRequest: WifConfigPatchRequest = #{
  spec: #{
    version: "4.18",
    project_id: "my-gcp-project-456",
  },
  labels: #{ environment: "staging" },
};
