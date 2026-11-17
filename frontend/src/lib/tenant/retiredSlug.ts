// When a retired tenant slug stops being reserved, read off the
// ListRetiredTenantSlugs contract.
import { timestampDate } from "@bufbuild/protobuf/wkt";

import type { RetiredTenantSlug } from "@/gen/paladin/admin/v1/tenant_service_pb";

/**
 * When the worker releases r: its release_time, set from the server's
 * admin.tenants.slug_reservation_period. null when no period is set, and the
 * slug is kept until an operator releases it.
 */
export function retiredSlugReleaseDate(r: RetiredTenantSlug): Date | null {
  return r.releaseTime ? timestampDate(r.releaseTime) : null;
}
