import { createClient } from "@connectrpc/connect";

import { dataTransport, iamTransport, adminTransport } from "./transport";

// admin plane
import { ObjectKeyService } from "@/gen/paladin/admin/v1/object_key_service_pb";
import { BucketService } from "@/gen/paladin/admin/v1/bucket_service_pb";
import { TenantService } from "@/gen/paladin/admin/v1/tenant_service_pb";
import { PolicyService } from "@/gen/paladin/admin/v1/policy_service_pb";
import { CELService } from "@/gen/paladin/admin/v1/cel_service_pb";
import { BackendService } from "@/gen/paladin/admin/v1/backend_service_pb";
import { QuotaService } from "@/gen/paladin/admin/v1/quota_service_pb";
import { AuditLogService } from "@/gen/paladin/admin/v1/audit_service_pb";
import { EventSubscriptionService } from "@/gen/paladin/admin/v1/event_subscription_service_pb";
import { OperationService as AdminOperationService } from "@/gen/paladin/admin/v1/operation_service_pb";
import { SystemService as AdminSystemService } from "@/gen/paladin/admin/v1/system_service_pb";
import { APITokenService } from "@/gen/paladin/admin/v1/api_token_service_pb";
import { CapabilityService } from "@/gen/paladin/admin/v1/capability_service_pb";
import { TenantBudgetService } from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import { MCPInspectService } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";

// data plane
import { ObjectService } from "@/gen/paladin/data/v1/object_service_pb";
import { ObjectTagService } from "@/gen/paladin/data/v1/object_tag_service_pb";
import { BatchService } from "@/gen/paladin/data/v1/batch_service_pb";
import { MultipartUploadService } from "@/gen/paladin/data/v1/multipart_service_pb";
import { PresignService } from "@/gen/paladin/data/v1/presign_service_pb";
import { OperationService as DataOperationService } from "@/gen/paladin/data/v1/operation_service_pb";

// iam plane
import { AuthService } from "@/gen/paladin/iam/v1/auth_service_pb";
import { UserService } from "@/gen/paladin/iam/v1/user_service_pb";
import { ApiKeyService } from "@/gen/paladin/iam/v1/api_key_service_pb";
import { UserSettingsService } from "@/gen/paladin/iam/v1/user_settings_service_pb";

// admin
export const objectKeyClient = createClient(ObjectKeyService, adminTransport);
export const bucketClient = createClient(BucketService, adminTransport);
export const tenantClient = createClient(TenantService, adminTransport);
export const policyClient = createClient(PolicyService, adminTransport);
export const celClient = createClient(CELService, adminTransport);
export const backendClient = createClient(BackendService, adminTransport);
export const quotaClient = createClient(QuotaService, adminTransport);
export const auditClient = createClient(AuditLogService, adminTransport);
export const eventSubscriptionClient = createClient(
  EventSubscriptionService,
  adminTransport,
);
export const adminOperationClient = createClient(
  AdminOperationService,
  adminTransport,
);
export const adminSystemClient = createClient(
  AdminSystemService,
  adminTransport,
);
export const apiTokenClient = createClient(APITokenService, adminTransport);
export const capabilityClient = createClient(CapabilityService, adminTransport);
export const tenantBudgetClient = createClient(
  TenantBudgetService,
  adminTransport,
);
export const mcpInspectClient = createClient(MCPInspectService, adminTransport);

// data
export const objectClient = createClient(ObjectService, dataTransport);
export const objectTagClient = createClient(ObjectTagService, dataTransport);
export const batchClient = createClient(BatchService, dataTransport);
export const multipartClient = createClient(
  MultipartUploadService,
  dataTransport,
);
export const presignClient = createClient(PresignService, dataTransport);
export const operationClient = createClient(
  DataOperationService,
  dataTransport,
);

// iam
import { SystemService } from "@/gen/paladin/iam/v1/system_service_pb";

export const authClient = createClient(AuthService, iamTransport);
export const userClient = createClient(UserService, iamTransport);
export const apiKeyClient = createClient(ApiKeyService, iamTransport);
export const userSettingsClient = createClient(
  UserSettingsService,
  iamTransport,
);
export const systemClient = createClient(SystemService, iamTransport);
