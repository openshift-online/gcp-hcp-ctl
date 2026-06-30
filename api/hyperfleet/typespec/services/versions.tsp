import "@typespec/http";
import "@typespec/openapi";
import "@typespec/openapi3";

import "../models/version/model.tsp";
import "hyperfleet/shared/models/common/model.tsp";

using Http;
using OpenAPI;

namespace HyperFleet;

@tag("Versions")
@useAuth(HyperFleet.BearerAuth)
interface Versions {
  /**
   * Returns a paginated list of versions for a channel.
   */
  @route("/channels/{channel_id}/versions")
  @get
  @summary("List versions for channel")
  @operationId("getVersionsByChannelId")
  getVersionsByChannelId(
    @path channel_id: string,
    ...QueryParams,
  ): Body<VersionList>
    | Error
    | BadRequestResponse;

  /**
   * Creates a new version under a channel.
   */
  @route("/channels/{channel_id}/versions")
  @post
  @summary("Create version")
  @operationId("postVersion")
  postVersion(
    @path channel_id: string,
    @body body: VersionCreateRequest,
  ): {
    @statusCode statusCode: 201;
    @body version: Version;
  } | Error
    | BadRequestResponse;

  /**
   * Returns a single version by its ID within a channel.
   */
  @route("/channels/{channel_id}/versions/{version_id}")
  @get
  @summary("Get version by ID")
  @operationId("getVersionById")
  getVersionById(
    @path channel_id: string,
    @path version_id: string,
  ): Version
    | Error
    | NotFoundResponse
    | BadRequestResponse;

  /**
   * Patch a version by ID. Supports partial updates to spec and labels.
   */
  @route("/channels/{channel_id}/versions/{version_id}")
  @patch
  @summary("Patch version by ID")
  @operationId("patchVersionById")
  patchVersionById(
    @path channel_id: string,
    @path version_id: string,
    @body body: VersionPatchRequest,
  ): Version
    | Error
    | NotFoundResponse
    | BadRequestResponse
    | ConflictResponse;

  /**
   * Marks a version for deletion.
   */
  @route("/channels/{channel_id}/versions/{version_id}")
  @delete
  @summary("Request version deletion")
  @operationId("deleteVersionById")
  deleteVersionById(
    @path channel_id: string,
    @path version_id: string,
  ): {
    @statusCode statusCode: 202;
    @body version: Version;
  } | NotFoundResponse
    | ConflictResponse
    | Error
    | BadRequestResponse;

}
