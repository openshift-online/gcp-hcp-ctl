import "@typespec/http";
import "@typespec/openapi";
import "@typespec/openapi3";

import "../models/channel/model.tsp";
import "hyperfleet/shared/models/common/model.tsp";

using Http;
using OpenAPI;

namespace HyperFleet;

@tag("Channels")
@route("/channels")
@useAuth(HyperFleet.BearerAuth)
interface Channels {
  /**
   * Returns a paginated list of channels, optionally filtered by labels or TSL query.
   */
  @get
  @route("")
  @summary("List channels")
  @operationId("getChannels")
  getChannels(...QueryParams): Body<ChannelList>
    | Error
    | BadRequestResponse;

  /**
   * Returns a single channel by its ID.
   */
  @route("/{channel_id}")
  @get
  @summary("Get channel by ID")
  @operationId("getChannelById")
  getChannelById(
    ...SearchParams,
    @path channel_id: string,
  ): Channel
    | Error
    | NotFoundResponse
    | BadRequestResponse;

  /**
   * Creates a new channel.
   */
  @route("")
  @post
  @summary("Create channel")
  @operationId("postChannel")
  postChannel(@body body: ChannelCreateRequest): {
    @statusCode statusCode: 201;
    @body channel: Channel;
  } | Error
    | BadRequestResponse;

  /**
   * Patches a channel by ID. Supports partial updates to spec and labels.
   */
  @route("/{channel_id}")
  @patch
  @summary("Patch channel by ID")
  @operationId("patchChannelById")
  patchChannelById(
    @path channel_id: string,
    @body body: ChannelPatchRequest,
  ): Channel
    | Error
    | NotFoundResponse
    | BadRequestResponse
    | ConflictResponse;

  /**
   * Marks the channel for deletion. Returns 409 if versions still exist (RESTRICT policy).
   */
  @route("/{channel_id}")
  @delete
  @summary("Request channel deletion")
  @operationId("deleteChannelById")
  deleteChannelById(
    @path channel_id: string,
  ): {
    @statusCode statusCode: 202;
    @body channel: Channel;
  } | NotFoundResponse
    | ConflictResponse
    | Error
    | BadRequestResponse;

}
