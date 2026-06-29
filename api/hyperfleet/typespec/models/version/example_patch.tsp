import "./model.tsp";

const exampleVersionPatchRequest: VersionPatchRequest = #{
  spec: #{
    raw_version: "4.17.12",
    enabled: false,
    is_default: false,
    release_image: "quay.io/openshift-release-dev/ocp-release:4.17.12-multi",
  },
  labels: #{ deprecated: "true" },
};
