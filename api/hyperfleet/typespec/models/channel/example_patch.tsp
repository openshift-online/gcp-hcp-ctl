import "./model.tsp";

const exampleChannelPatchRequest: ChannelPatchRequest = #{
  spec: #{
    is_default: false,
    enabled_regex: "^4\\.17\\.\\d+$",
  },
  labels: #{ tier: "staging" },
};
