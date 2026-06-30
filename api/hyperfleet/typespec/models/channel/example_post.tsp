import "./model.tsp";

const exampleChannelCreateRequest: ChannelCreateRequest = #{
  kind: "Channel",
  name: "stable",
  labels: #{ tier: "production" },
  spec: #{
    is_default: true,
    enabled_regex: "^4\\.\\d+\\.\\d+$",
  },
};
