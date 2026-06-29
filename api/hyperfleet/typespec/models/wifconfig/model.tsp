import "@typespec/openapi";
import "hyperfleet/shared/models/common/model.tsp";
import "hyperfleet/shared/models/statuses/model.tsp";
import "hyperfleet/shared/models/resource/model.tsp";
import "./example_wifconfig.tsp";
import "./example_post.tsp";
import "./example_patch.tsp";

using OpenAPI;

alias KindWifConfig = "WifConfig";

model WifConfigSpec {
  /** Version of the WIF configuration */
  version: string;

  /** GCP project identifier */
  project_id: string;
}

model WifConfigBase {
  ...APIResource;

  @minLength(1)
  @maxLength(63)
  @pattern("^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")
  name: string;

  spec: WifConfigSpec;
}

@example(exampleWifConfig)
model WifConfig {
  ...WifConfigBase;
  ...APIMetadata;

  @minValue(1)
  generation: int32;

  status: ResourceStatus;
}

@example(exampleWifConfigCreateRequest)
model WifConfigCreateRequest {
  ...WifConfigBase;
}

@extension("minProperties", 1)
@extension("additionalProperties", false)
@example(exampleWifConfigPatchRequest)
model WifConfigPatchRequest {
  spec?: WifConfigSpec;
  labels?: Record<string>;
}

model WifConfigList {
  ...List<WifConfig>;
}
