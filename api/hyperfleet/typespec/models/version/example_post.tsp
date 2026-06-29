import "./model.tsp";

const exampleVersionCreateRequest: VersionCreateRequest = #{
  kind: "Version",
  name: "4.17.12",
  labels: #{},
  spec: #{
    raw_version: "4.17.12",
    enabled: true,
    is_default: true,
    release_image: "quay.io/openshift-release-dev/ocp-release:4.17.12-multi",
  },
};
