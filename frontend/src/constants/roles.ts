/**
 * Roles a Cedar policy can key on, as the backend defines them
 * (internal/api/apiutil). Kept in one place so a dialog and a badge
 * cannot disagree about what a role is called.
 *
 * These are the names, not the authority: what each one may do is decided
 * by policy, and the server is the only thing that enforces it.
 */
export const ROLES = {
  platformAdmin: "platform.admin",
  tenantProvisioner: "platform.tenant-provisioner",
  capabilityIssuer: "platform.capability-issuer",
  iamAdmin: "iam.admin",
  bucketAdmin: "bucket.admin",
  tenantAdmin: "tenant.admin",
  tenantUser: "tenant.user",
  mcpOperator: "mcp.operator",
} as const;

/**
 * Offered when creating or editing a user. `platform.admin` is deliberately
 * absent: granting the role that can grant every other role is not a
 * checkbox decision, and the API still accepts it for an operator who means
 * it.
 */
export const ASSIGNABLE_ROLES: readonly string[] = [
  ROLES.tenantUser,
  ROLES.tenantAdmin,
  ROLES.bucketAdmin,
  ROLES.iamAdmin,
  ROLES.mcpOperator,
  ROLES.tenantProvisioner,
  ROLES.capabilityIssuer,
];

/**
 * IAM issues the paladin-admin audience only to a principal holding a role
 * that ends in this (authh.isAdminRole); a pure tenant.user never gets one.
 */
export const ADMIN_TIER_ROLE_SUFFIX = ".admin";

/**
 * Whether the admin plane is reachable at all for these roles. The console
 * uses it to leave out what would only fail; the server still decides every
 * call.
 */
export function canUseAdminPlane(roles: readonly string[]): boolean {
  return roles.some((r) => r.endsWith(ADMIN_TIER_ROLE_SUFFIX));
}
