"use client";

import { TabStub } from "../_TabStub";

export default function TenantEventSubscriptionsPage() {
  return (
    <TabStub
      title="Event subscriptions"
      description="Outbound event sinks (HTTP / NATS) for this tenant."
      legacyHref="/events"
      legacyLabel="/events"
    />
  );
}
