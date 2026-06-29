model NodePoolSpec {
    @minValue(0) replicas?: integer;
    platform: NodePoolPlatformSpec;
    release?: NodePoolReleaseSpec;
}

model NodePoolPlatformSpec {
    type: "gcp";
    gcp: GCPNodePoolPlatform;
}

model GCPNodePoolPlatform {
    /** GCE machine type (e.g. n2-standard-4). Maps to HyperShift NodePool machineType. */
    instanceType?: string;

    /** Boot disk configuration. Maps to HyperShift NodePool bootDisk. */
    rootVolume?: GCPRootVolume;

    /** GCP zone for node placement (e.g. us-central1-a). Falls back to cluster region + "-a" if not set. */
    zone?: string;
}

model GCPRootVolume {
    /** Boot disk size in GB. Maps to HyperShift bootDisk.diskSizeGB. */
    @minValue(10) size?: integer;

    /** Boot disk type. Maps to HyperShift bootDisk.diskType. */
    type?: "pd-standard" | "pd-ssd" | "pd-balanced";
}

model NodePoolReleaseSpec {
    /** OCP version string (e.g. 4.22.0-rc.5). Resolved to a release image by nodepool-vr-adapter via Cincinnati. */
    version?: string;

    /** Cincinnati channel group (e.g. candidate, stable). Combined with version major.minor to derive channel. */
    channelGroup?: string;
}
