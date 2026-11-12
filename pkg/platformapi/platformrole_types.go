package platformapi

import gcpv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"

// PlatformRole is the public, platform-scoped catalog wire type.
// Use the SDK type so the shared serializer registers each kind only once.
type PlatformRole = gcpv1.PlatformRole

// PlatformRoleSpec contains the complete permission set returned by the server.
type PlatformRoleSpec = gcpv1.PlatformRoleSpec

// PlatformRoleList is the Kubernetes-shaped public catalog response.
type PlatformRoleList = gcpv1.PlatformRoleList
