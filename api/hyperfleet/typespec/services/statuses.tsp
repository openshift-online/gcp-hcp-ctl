import "@typespec/http";
import "@typespec/openapi";
import "@typespec/openapi3";

import "hyperfleet/shared/models/statuses/model.tsp";
import "hyperfleet/shared/models/common/model.tsp";

using Http;
using OpenAPI;

namespace HyperFleet;

@tag("Clusters")
@route("/clusters/{cluster_id}/statuses")
@useAuth(HyperFleet.BearerAuth)
interface ClusterStatuses {
  /**
   * Returns all adapter status reports for the given cluster.
   */
  @get
  @route("")
  @summary("List adapter statuses for cluster")
  @operationId("getClusterStatuses")
  getClusterStatuses(
    @path cluster_id: string,
  ): Body<AdapterStatusList>
    | Error
    | UnauthorizedResponse
    | NotFoundResponse;
}
