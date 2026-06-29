import "@typespec/openapi";
import "hyperfleet/shared/models/common/model.tsp";
import "hyperfleet/shared/models/statuses/model.tsp";
import "hyperfleet/shared/models/resource/model.tsp";
import "./example_channel.tsp";
import "./example_post.tsp";
import "./example_patch.tsp";

using OpenAPI;

alias KindChannel = "Channel";

model ChannelSpec {
  /** Whether this is the default channel for new clusters */
  is_default: boolean;

  /** Regex pattern for matching enabled version strings */
  enabled_regex: string;
}

model ChannelBase {
  ...APIResource;

  @minLength(1)
  @maxLength(63)
  @pattern("^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")
  name: string;

  spec: ChannelSpec;
}

@example(exampleChannel)
model Channel {
  ...ChannelBase;
  ...APIMetadata;

  @minValue(1)
  generation: int32;

  status: ResourceStatus;
}

@example(exampleChannelCreateRequest)
model ChannelCreateRequest {
  ...ChannelBase;
}

@extension("minProperties", 1)
@extension("additionalProperties", false)
@example(exampleChannelPatchRequest)
model ChannelPatchRequest {
  spec?: ChannelSpec;
  labels?: Record<string>;
}

model ChannelList {
  ...List<Channel>;
}
