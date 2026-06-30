model ClusterSpec {
    infraID?: string;
    issuerURL?: string;
    platform: ClusterPlatformSpec;
    release?: ReleaseSpec;
    networking?: NetworkingSpec;
    dns?: DNSSpec;
}

model ClusterPlatformSpec {
    type: "gcp";
    gcp: GCPClusterPlatform;
}

model GCPClusterPlatform {
    projectID: string;
    region: string;
    network?: string;
    subnet?: string;
    endpointAccess?: "Private" | "PublicAndPrivate";
    workloadIdentity?: GCPWorkloadIdentity;
}

model GCPWorkloadIdentity {
    projectNumber: string;
    poolID: string;
    providerID: string;
    serviceAccountsRef?: GCPServiceAccountsRef;
}

model GCPServiceAccountsRef {
    controlPlaneEmail?: string;
    nodePoolEmail?: string;
    cloudControllerEmail?: string;
    storageEmail?: string;
    imageRegistryEmail?: string;
    networkEmail?: string;
}

model ReleaseSpec {
    image?: string;
    version?: string;
    channelGroup?: string;
}

model NetworkingSpec {
    clusterNetwork?: ClusterNetworkEntry[];
    serviceNetwork?: string[];
}

model ClusterNetworkEntry {
    cidr?: string;
    hostPrefix?: integer;
}

model DNSSpec {
    baseDomain?: string;
}
