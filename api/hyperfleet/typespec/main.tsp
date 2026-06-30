import "@typespec/http";
import "@typespec/openapi";
import "@typespec/openapi3";

import "./models/cluster/model.tsp";
import "./models/cluster/example_cluster.tsp";
import "./models/cluster/example_post.tsp";
import "./models/cluster/example_patch.tsp";
import "./models/nodepool/model.tsp";
import "./models/nodepool/example_nodepool.tsp";
import "./models/nodepool/example_post.tsp";
import "./models/nodepool/example_patch.tsp";
import "./models/channel/model.tsp";
import "./models/version/model.tsp";
import "./models/wifconfig/model.tsp";

import "hyperfleet/shared/services/clusters.tsp";
import "hyperfleet/shared/services/nodepools.tsp";
import "./services/channels.tsp";
import "./services/statuses.tsp";
import "./services/versions.tsp";
import "./services/wifconfigs.tsp";

using Http;
using OpenAPI;

/**
 * HyperFleet GCP API provides CRUD operations for managing OpenShift hosted control plane
 * clusters on Google Cloud Platform.
 *
 * **Architecture**: Simple CRUD only, no business logic, no event creation.
 * Sentinel operator handles all orchestration logic.
 * Adapters handle the specifics of managing spec.
 *
 */
@service(#{ title: "HyperFleet GCP API" })
@info(#{
  version: "1.0.0",
  contact: #{
    name: "GCP HCP Team",
    url: "https://github.com/openshift-online/gcp-hcp-ctl",
  },
  license: #{
    name: "Apache 2.0",
    url: "https://www.apache.org/licenses/LICENSE-2.0",
  },
})
@server("https://hyperfleet-api.gcp-hcp.devshift.net", "Production")
@route("/api/hyperfleet/v1")
namespace HyperFleet;

// Override BearerAuth to use lowercase "bearer" as required by kin-openapi
model BearerAuth {
  type: AuthType.http;
  scheme: "bearer";
}
