package platformapi

import gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"

// PlatformRole uses the SDK's registered catalog type while preserving the
// platformapi client's existing type name.
type PlatformRole = gcpv1.PlatformRole

// PlatformRoleSpec contains the complete permission set returned by the server.
type PlatformRoleSpec = gcpv1.PlatformRoleSpec

// PlatformRoleList is the Kubernetes-shaped public catalog response.
type PlatformRoleList = gcpv1.PlatformRoleList
