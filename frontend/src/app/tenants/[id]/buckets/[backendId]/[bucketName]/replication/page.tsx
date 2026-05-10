"use client";

import { BucketTabStub } from "../_BucketTabStub";

export default function BucketReplicationPage() {
  return (
    <BucketTabStub
      title="Replication"
      description={
        "Cross-region / cross-backend mirror configuration. The " +
        "control-plane API isn't wired yet — this tab lands together " +
        "with the BucketReplication proto + replicator worker."
      }
    />
  );
}
