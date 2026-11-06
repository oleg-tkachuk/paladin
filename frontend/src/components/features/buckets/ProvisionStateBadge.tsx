import { StatusBadge } from "@/components/ui/StatusBadge";
import { provisionStatus } from "@/lib/bucketProvision";

/** A bucket's provisioning state, as the platform and tenant tables show it. */
export function ProvisionStateBadge({ state }: { state: string }) {
  const { label, tone } = provisionStatus(state);
  return <StatusBadge tone={tone}>{label}</StatusBadge>;
}
