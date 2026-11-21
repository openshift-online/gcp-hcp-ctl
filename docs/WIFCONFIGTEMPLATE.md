# Catalog-driven IAM: code-first candidate

This branch implements the Phase 3 CLI candidate. **It is not live approval.**
Do not enable template verification/assertion writes or allocate a stack until the
separate project/resource/cost/retention manifest has been approved.

## Build dependency boundary

The currently published Gecko modules pinned in go.mod predate this feature.
Development tests/builds require an external workspace containing this CLI and
the matching Gecko `platform-api` and `orlop` checkouts. Record exact SHAs and set
GOWORK explicitly. Never commit local replace paths or claim this branch builds
standalone/in release CI until compatible published dependencies are selected.

The CLI directly uses only public API types/endpoints. The generated public API
module itself imports private conversion types transitively; that upstream coupling
is not removed here. No server entry point or private RPC client is introduced.

## Workflow

### Inspecting the catalog (read-only)

`gcphcpctl wifconfigtemplate list` shows template boundaries, verification state
and the canonical contract digest. `gcphcpctl wifconfigtemplate get 5.0.0 -o yaml`
shows the full public IAM contract. Both use the authenticated regional platform
API and require the usual project/region or explicit API endpoint configuration.
JSON/YAML return API resources without managed fields; the computed digest appears
in text output only, not as an extra API field. These commands never change
template verification or Cluster application history.

### Existing-Cluster IAM updates (Phase 4 code-first; no live approval)

`gcphcpctl iam update <cluster-name> --version <exact-release>
--channel-group <group> --installation-state <protected-primary-journal>
--dry-run` is **read-only**. It cannot apply a role update or append a Cluster
application. It requires the
original confirmed installation journal, complete fresh Cluster/NodePool IAM
requirements, a full NodePool list and independently matching original cloud
IAM. It rejects deleting resources, changed ownership, stale status or missing
contracts. It also re-reads the object inventory and target catalog after
cloud verification to detect races during preview.

The preview retains **every** append-only Cluster application contract as a
conservative upper bound, including unverified historical entries; a Pending
receipt cannot exist without the corresponding Cluster application assertion.
It only proposes additions to existing custom-role permission sets when all
identities, bindings and federation trust remain identical. `targetEligibleForNewUse`
and `targetBlock` describe catalog eligibility, not permission to execute the
preview's `planFingerprint`. A preview alone never executes anything. Historic
Versions may age out of the catalog; this initial preview still requires the
original journal's Version UID to exist to confirm the API realm and fails
closed if it cannot be read.

After separate live approval, replace `--dry-run` with
`--confirm --expect-plan <reviewed-fingerprint>` and the same exact target,
channel group and original installation journal. Only custom-role permission
additions and the exact Cluster application assertion are written. No resource
creation, IAM policy write, trust change, permission pruning or release request
is performed. The GCP role patch uses only `includedPermissions`, with a fresh
etag; title, description/ownership and stage are not changed. The caller needs
`iam.roles.get` and `iam.roles.update` in addition to the preview's read access.

The private linked state is fixed at `<installation-state>.updates.json`.
Keep it alongside the **unchanged original** journal, using a single operator
and one authoritative copy of both. The shared local installation lock prevents
cooperating commands from concurrently updating/cleaning that installation; it
is not a distributed lock between machines. Initial state publication uses a
fully written/fsynced private file and an exclusive hard link; filesystems
without hard-link support fail closed. Subsequent writes use atomic replacement
and directory fsync. Legacy create/resume commands refuse a primary with linked
state; `iam verify` and `iam destroy` automatically follow that sidecar.

Resume an interrupted operation using `--confirm --resume --expect-plan
<original-fingerprint>`. Role intent precedes every RPC, and resume reads exact
before/after content and etags. A 404 never permits role recreation. Assertion
submission uses JSONPatch tests for the Cluster UID and a freshly read resource
version. If cloud verification succeeded but the assertion response failed,
the operation stays incomplete until independent assertion readback succeeds.
Same-contract coverage needs no extra assertion. The stable fingerprint includes
UIDs, specs and exact retained contracts, not periodic status-only resource
versions. Fresh status and inventory are still checked before writes.

`payloadCatalogBinding` binds the target Version UID and generation. Payload
pullspecs remain private; a private Version spec/payload edit advances generation.
This is an indirect catalog pin, **not** a registry digest or image attestation.

An operation still in Prepared with every role Planned can be explicitly
tombstoned using `--confirm --resume --abandon-unstarted --expect-plan
<original-fingerprint>`. The intent is retained, not deleted. Once any role write
has been attempted, this escape is refused. Unexpected ownership, etag or
permission drift after an Intent/Verified step requires a separate reviewed
recovery: retain both journals, stop all writers, independently inspect the exact
roles and API assertions, and do not edit the journal, adopt a superset, force
an assertion or allocate a second operation to bypass the conflict. There is no
automatic force-unlock. Losing an original or linked journal is not evidence
that an operation had no effect.

Verification reports both cloud readback and update completion; the former alone
does not imply the assertion was recorded. Normal hosted teardown must finish
before `iam destroy --dry-run`/confirmed cleanup. Updated role unions and actual
etags are checked before removing owned grants/resources. Cleanup progress,
including owned-network teardown, is written only to the linked sidecar. Old
permissions are never pruned as an update or cleanup-preparation shortcut.

Use explicitly approved project, region and discovery/API/OIDC settings. Examples
describe the new interface; placeholder values are not permission to mutate cloud.

1. Preview an exact release with `iam create <infra-id> --version <release>
   --channel-group <group> --dry-run`, or preview the full installation using
   `cluster create <name> --setup-infra --infra-id <id> --version <release>
   --vpc-cidr <cidr> --dry-run`.
2. Inspect the contract reference, exact scopes/members, desired resources and
   `planFingerprint`. Unverified/unavailable targets remain non-executable.
3. Only after approval, use `--confirm --expect-plan <fingerprint> --state-file
   <private-directory>/state.json`. The parent directory must be 0700, files 0600,
   owned by the caller, with no symlink components. Prefer canonical temporary
   paths on macOS (`/private/var/...`, not its `/var` alias).
4. Reuse that state file with explicit `--resume`. Never regenerate an infra ID or
   use a new state file to bypass a collision/unknown result. A confirmed Cluster
   can be resolved read-only after eligibility revocation; no new work is authorized.
5. `iam verify <infra-id> --state-file ...` independently reads catalog/cloud state.
   It makes no API assertion and does not update state. A saved config is not proof.

The issuer is the selected discovered/configured regional issuer plus the infra ID.
Inline JWKS is rejected by the new flow. Schema v1 has no audience/mapping fields;
the existing `openshift` audience and `google.subject=assertion.sub` mapping are
explicit plan/journal execution conventions outside the template digest.

The config-file mode requires the owned state file and fresh verification. Legacy
broad-role IAM configs are rejected. An explicitly supplied network config is
read-only external input, verified by exact shape and numeric IDs, never adopted
or made a cleanup target. All file/flag identities must agree before mutation.

Use `--maintenance-exclusion-until <RFC3339>` for an agreed initial no-upgrade period.
The reviewed start time is journaled and reused across midnight on resume. A changed
or expired plan needs review; there is no critical/maintenance override bypass.

## Safety and partial outcomes

- No embedded permission bundle, automatic undelete/enable, trust repair, broad-role
  fallback, SA keys, project-wide actAs or default Compute SA creation/deletion.
- Mutations recheck current Version/template eligibility and exact content. Grants
  preserve policy-v3, conditions, unrelated members/audit settings and optimistic
  etags. Readback, not a successful Create/Set response, precedes success output.
- The journal records intent before RPC, operation IDs before waiting and immutable
  identities after readback. No tokens/private keys/raw policies/provider prose.
- Definitive no-effect rejections can resume. Timeouts, conflicts and unknown
  outcomes resolve exact owned state, never adopt a name or allocate a second stack.
- An unknown Cluster POST followed by absence stays blocked. Name uniqueness alone
  cannot prove that a first accepted object was not subsequently deleted; no automatic
  re-POST or IAM cleanup is performed on that evidence alone. Operator reconciliation
  is required for this conservative case.

## Cleanup

Delete hosted resources normally first. `cluster delete` initiates deletion; it is
not proof that deletion finished. `iam destroy <id> --state-file ... --dry-run`
checks real teardown/dependency conditions and reports whether cleanup is eligible.
Actual cleanup needs explicit confirmation and the same journal. Add
`--include-owned-network --yes` only after reviewing the recorded network footprint.

Confirmed submissions require repeated API absence, complete reference checks and
customer-cloud dependency reads. Unknown submissions are never inferred gone by
polling. Cleanup removes only managed membership deltas and proven-owned resources;
it does not delete a Cluster, force a finalizer or touch the default worker SA.
Explicit external networks are retained. Unmarked legacy standalone network behavior
is not redesigned, but its commands refuse journal-marked stacks; do not use them
for this workflow.

The journal pins the original credential-free API endpoint. Cleanup additionally
requires that endpoint's retained Version UID, regardless of present availability.
A different realm, replaced/missing Version or proxy/bare HTTP 404 is not teardown
proof. Resource absence requires an explicit API Status response.

SA deletion targets its unique ID; role deletion uses its etag. Compute network
deletes and cross-resource API checks are not atomic UID locks: reads narrow races,
not close them against another authorized actor. Changed ownership/IDs, missing or
recreated worker identity, unreadable/partial inventories and unprovable operations
are conservative refusals, not reasons to widen permissions or delete by prefix.

Retain successful resources for Phase 4 only under a separately agreed deadline,
cost ceiling and cleanup responsibility. Code-first completion is not Phase 3 live
completion, and budget alerts are not hard spending caps.
