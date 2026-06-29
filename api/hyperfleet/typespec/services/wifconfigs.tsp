import "@typespec/http";
import "@typespec/openapi";
import "@typespec/openapi3";

import "../models/wifconfig/model.tsp";
import "hyperfleet/shared/models/common/model.tsp";

using Http;
using OpenAPI;

namespace HyperFleet;

@tag("WifConfigs")
@route("/wifconfigs")
@useAuth(HyperFleet.BearerAuth)
interface WifConfigs {
  /**
   * Returns a paginated list of WIF configs, optionally filtered by labels or TSL query.
   */
  @get
  @route("")
  @summary("List WIF configs")
  @operationId("getWifConfigs")
  getWifConfigs(...QueryParams): Body<WifConfigList>
    | Error
    | BadRequestResponse;

  /**
   * Returns a single WIF config by its ID.
   */
  @route("/{wifconfig_id}")
  @get
  @summary("Get WIF config by ID")
  @operationId("getWifConfigById")
  getWifConfigById(
    ...SearchParams,
    @path wifconfig_id: string,
  ): WifConfig
    | Error
    | NotFoundResponse
    | BadRequestResponse;

  /**
   * Creates a new WIF config.
   */
  @route("")
  @post
  @summary("Create WIF config")
  @operationId("postWifConfig")
  postWifConfig(@body body: WifConfigCreateRequest): {
    @statusCode statusCode: 201;
    @body wifconfig: WifConfig;
  } | Error
    | BadRequestResponse;

  /**
   * Patches a WIF config by ID. Supports partial updates to spec and labels.
   */
  @route("/{wifconfig_id}")
  @patch
  @summary("Patch WIF config by ID")
  @operationId("patchWifConfigById")
  patchWifConfigById(
    @path wifconfig_id: string,
    @body body: WifConfigPatchRequest,
  ): WifConfig
    | Error
    | NotFoundResponse
    | BadRequestResponse
    | ConflictResponse;

  /**
   * Marks a WIF config for deletion.
   */
  @route("/{wifconfig_id}")
  @delete
  @summary("Request WIF config deletion")
  @operationId("deleteWifConfigById")
  deleteWifConfigById(
    @path wifconfig_id: string,
  ): {
    @statusCode statusCode: 202;
    @body wifconfig: WifConfig;
  } | NotFoundResponse
    | ConflictResponse
    | Error
    | BadRequestResponse;

}
