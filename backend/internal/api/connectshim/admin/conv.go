// Package admin wires generated paladin.admin.v1 Connect server stubs onto the
// internal/api/admin/v1/<service>h handler packages and the existing
// tenant/object_key/policy/operation handlers from internal/api/v1/*.
package admin

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin-private/internal/api/admin/v1/admindomain"
	pb "github.com/oleg-tkachuk/paladin-private/internal/api/pb/admin/v1"
	commonpb "github.com/oleg-tkachuk/paladin-private/internal/api/pb/common/v1"
)

// ─── ts helpers ─────────────────────────────────────────────────────────────

func tsProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func tsPtrProto(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}

func resourceVersion(v int64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatInt(v, 10)
}

func parseRV(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

func pageResponseProto(next string) *commonpb.PageResponse {
	if next == "" {
		return nil
	}
	return &commonpb.PageResponse{NextPageToken: next}
}

// ─── StorageBackend ↔ proto ─────────────────────────────────────────────────

func backendToProto(b *admindomain.StorageBackend) *pb.StorageBackend {
	if b == nil {
		return nil
	}
	return &pb.StorageBackend{
		Name:                          fmt.Sprintf("storageBackends/%s", b.BackendID),
		BackendId:                     b.BackendID,
		DisplayName:                   b.DisplayName,
		Kind:                          storageKindProto(b.Kind),
		Provider:                      b.Provider,
		Endpoint:                      b.Endpoint,
		PublicEndpoint:                b.PublicEndpoint,
		Region:                        b.Region,
		ForcePathStyle:                b.ForcePathStyle,
		CredentialsSecretRef:          b.CredentialsSecretRef,
		Sse:                           sseToProto(b.SSE),
		Events:                        eventsToProto(b.Events),
		CedarPolicy:                   b.CedarPolicy,
		Enabled:                       b.Enabled,
		ReadOnly:                      b.ReadOnly,
		Maintenance:                   b.Maintenance,
		HealthStatus:                  b.HealthStatus,
		HealthMessage:                 b.HealthMessage,
		HealthCheckedAt:               tsProto(b.HealthCheckedAt),
		PreviousCredentialsSecretRef:  b.PreviousCredentialsSecretRef,
		PreviousCredentialsValidUntil: tsProto(b.PreviousCredentialsValidUntil),
		ResourceVersion:               resourceVersion(b.ResourceVersion),
		CreatedAt:                     tsProto(b.CreatedAt),
		UpdatedAt:                     tsProto(b.UpdatedAt),
	}
}

func backendFromProto(p *pb.StorageBackend) admindomain.StorageBackend {
	if p == nil {
		return admindomain.StorageBackend{}
	}
	return admindomain.StorageBackend{
		BackendID:            p.GetBackendId(),
		DisplayName:          p.GetDisplayName(),
		Kind:                 storageKindFromProto(p.GetKind()),
		Provider:             p.GetProvider(),
		Endpoint:             p.GetEndpoint(),
		PublicEndpoint:       p.GetPublicEndpoint(),
		Region:               p.GetRegion(),
		ForcePathStyle:       p.GetForcePathStyle(),
		CredentialsSecretRef: p.GetCredentialsSecretRef(),
		SSE:                  sseFromProto(p.GetSse()),
		Events:               eventsFromProto(p.GetEvents()),
		CedarPolicy:          p.GetCedarPolicy(),
	}
}

func storageKindProto(s string) pb.StorageKind {
	switch s {
	case "aws-s3":
		return pb.StorageKind_STORAGE_KIND_AWS_S3
	case "s3-compatible":
		return pb.StorageKind_STORAGE_KIND_S3_COMPATIBLE
	case "gcs":
		return pb.StorageKind_STORAGE_KIND_GCS
	}
	return pb.StorageKind_STORAGE_KIND_UNSPECIFIED
}

func storageKindFromProto(k pb.StorageKind) string {
	switch k {
	case pb.StorageKind_STORAGE_KIND_AWS_S3:
		return "aws-s3"
	case pb.StorageKind_STORAGE_KIND_S3_COMPATIBLE:
		return "s3-compatible"
	case pb.StorageKind_STORAGE_KIND_GCS:
		return "gcs"
	}
	return ""
}

func sseToProto(s admindomain.ServerSideEncryption) *pb.ServerSideEncryption {
	out := &pb.ServerSideEncryption{KeyId: s.KeyID}
	switch s.Type {
	case "AES256":
		out.Type = pb.SseType_SSE_TYPE_AES256
	case "KMS":
		out.Type = pb.SseType_SSE_TYPE_KMS
	case "":
		out.Type = pb.SseType_SSE_TYPE_NONE
	}
	return out
}

func sseFromProto(p *pb.ServerSideEncryption) admindomain.ServerSideEncryption {
	if p == nil {
		return admindomain.ServerSideEncryption{}
	}
	out := admindomain.ServerSideEncryption{KeyID: p.GetKeyId()}
	switch p.GetType() {
	case pb.SseType_SSE_TYPE_AES256:
		out.Type = "AES256"
	case pb.SseType_SSE_TYPE_KMS:
		out.Type = "KMS"
	}
	return out
}

func eventsToProto(e admindomain.EventSourceConfig) *pb.EventSourceConfig {
	out := &pb.EventSourceConfig{
		Enabled:      e.Enabled,
		QueueUrl:     e.QueueURL,
		PollInterval: durationpb.New(e.PollInterval),
	}
	switch e.Target {
	case "sqs":
		out.Target = pb.EventTarget_EVENT_TARGET_SQS
	case "redis":
		out.Target = pb.EventTarget_EVENT_TARGET_REDIS
	default:
		out.Target = pb.EventTarget_EVENT_TARGET_NONE
	}
	return out
}

func eventsFromProto(p *pb.EventSourceConfig) admindomain.EventSourceConfig {
	if p == nil {
		return admindomain.EventSourceConfig{}
	}
	out := admindomain.EventSourceConfig{
		Enabled:      p.GetEnabled(),
		QueueURL:     p.GetQueueUrl(),
		PollInterval: p.GetPollInterval().AsDuration(),
	}
	switch p.GetTarget() {
	case pb.EventTarget_EVENT_TARGET_SQS:
		out.Target = "sqs"
	case pb.EventTarget_EVENT_TARGET_REDIS:
		out.Target = "redis"
	}
	return out
}

// ─── Bucket ↔ proto ─────────────────────────────────────────────────────────

func bucketToProto(b *admindomain.Bucket) *pb.Bucket {
	if b == nil {
		return nil
	}
	return &pb.Bucket{
		Name:            fmt.Sprintf("storageBackends/%s/buckets/%s", b.BackendID, b.BucketName),
		BackendId:       b.BackendID,
		BucketName:      b.BucketName,
		DisplayName:     b.DisplayName,
		Region:          b.Region,
		OwnerTenantId:   uuidStrEmpty(b.OwnerTenantID.String()),
		CedarPolicy:     b.CedarPolicy,
		Constraints:     constraintsToProto(b.Constraints),
		LifecycleRules:  lifecycleToProto(b.LifecycleRules),
		ObjectLock:      lockToProto(b.ObjectLock),
		Versioning:      versioningToProto(b.Versioning),
		Replication:     replicationToProto(b.Replication),
		Labels:          b.Labels,
		ResourceVersion: resourceVersion(b.ResourceVersion),
		CreatedAt:       tsProto(b.CreatedAt),
		UpdatedAt:       tsProto(b.UpdatedAt),
		ProvisionState:  b.ProvisionState,
	}
}

func uuidStrEmpty(s string) string {
	if s == "00000000-0000-0000-0000-000000000000" {
		return ""
	}
	return s
}

func constraintsToProto(c admindomain.BucketConstraints) *pb.BucketConstraints {
	out := &pb.BucketConstraints{
		MaxObjectSizeBytes:  c.MaxObjectSizeBytes,
		MinPartSizeBytes:    c.MinPartSizeBytes,
		MaxPartSizeBytes:    c.MaxPartSizeBytes,
		MaxParts:            c.MaxParts,
		AllowedContentTypes: c.AllowedContentTypes,
	}
	if c.MaxPresignPutTTL > 0 {
		out.MaxPresignPutTtl = durationpb.New(c.MaxPresignPutTTL)
	}
	if c.MaxPresignGetTTL > 0 {
		out.MaxPresignGetTtl = durationpb.New(c.MaxPresignGetTTL)
	}
	switch c.RequiredChecksumAlgorithm {
	case "CRC32C":
		out.RequiredChecksumAlgorithm = commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C
	case "SHA256":
		out.RequiredChecksumAlgorithm = commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256
	case "MD5":
		out.RequiredChecksumAlgorithm = commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_MD5
	}
	return out
}

func constraintsFromProto(p *pb.BucketConstraints) admindomain.BucketConstraints {
	if p == nil {
		return admindomain.BucketConstraints{}
	}
	out := admindomain.BucketConstraints{
		MaxObjectSizeBytes:  p.GetMaxObjectSizeBytes(),
		MinPartSizeBytes:    p.GetMinPartSizeBytes(),
		MaxPartSizeBytes:    p.GetMaxPartSizeBytes(),
		MaxParts:            p.GetMaxParts(),
		AllowedContentTypes: p.GetAllowedContentTypes(),
		MaxPresignPutTTL:    p.GetMaxPresignPutTtl().AsDuration(),
		MaxPresignGetTTL:    p.GetMaxPresignGetTtl().AsDuration(),
	}
	switch p.GetRequiredChecksumAlgorithm() {
	case commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C:
		out.RequiredChecksumAlgorithm = "CRC32C"
	case commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256:
		out.RequiredChecksumAlgorithm = "SHA256"
	case commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_MD5:
		out.RequiredChecksumAlgorithm = "MD5"
	}
	return out
}

func lifecycleToProto(rules []admindomain.LifecycleRule) []*pb.LifecycleRule {
	out := make([]*pb.LifecycleRule, 0, len(rules))
	for _, r := range rules {
		pr := &pb.LifecycleRule{Id: r.ID, Enabled: r.Enabled, Match: r.Match}
		if r.Transition != nil {
			pr.Action = &pb.LifecycleRule_Transition{
				Transition: &pb.LifecycleTransition{
					After:        durationpb.New(r.Transition.After),
					StorageClass: r.Transition.StorageClass,
				},
			}
		} else if r.Expiration != nil {
			pr.Action = &pb.LifecycleRule_Expiration{
				Expiration: &pb.LifecycleExpiration{After: durationpb.New(r.Expiration.After)},
			}
		}
		out = append(out, pr)
	}
	return out
}

func lifecycleFromProto(rules []*pb.LifecycleRule) []admindomain.LifecycleRule {
	out := make([]admindomain.LifecycleRule, 0, len(rules))
	for _, r := range rules {
		dr := admindomain.LifecycleRule{ID: r.GetId(), Enabled: r.GetEnabled(), Match: r.GetMatch()}
		if t := r.GetTransition(); t != nil {
			dr.Transition = &admindomain.LifecycleTransition{
				After:        t.GetAfter().AsDuration(),
				StorageClass: t.GetStorageClass(),
			}
		}
		if e := r.GetExpiration(); e != nil {
			dr.Expiration = &admindomain.LifecycleExpiration{After: e.GetAfter().AsDuration()}
		}
		out = append(out, dr)
	}
	return out
}

func lockToProto(l admindomain.ObjectLockConfig) *pb.ObjectLockConfig {
	out := &pb.ObjectLockConfig{
		Enabled:          l.Enabled,
		DefaultRetention: durationpb.New(l.DefaultRetention),
	}
	switch l.DefaultMode {
	case "GOVERNANCE":
		out.DefaultMode = pb.ObjectLockMode_OBJECT_LOCK_MODE_GOVERNANCE
	case "COMPLIANCE":
		out.DefaultMode = pb.ObjectLockMode_OBJECT_LOCK_MODE_COMPLIANCE
	}
	return out
}

func lockFromProto(p *pb.ObjectLockConfig) admindomain.ObjectLockConfig {
	if p == nil {
		return admindomain.ObjectLockConfig{}
	}
	out := admindomain.ObjectLockConfig{
		Enabled:          p.GetEnabled(),
		DefaultRetention: p.GetDefaultRetention().AsDuration(),
	}
	switch p.GetDefaultMode() {
	case pb.ObjectLockMode_OBJECT_LOCK_MODE_GOVERNANCE:
		out.DefaultMode = "GOVERNANCE"
	case pb.ObjectLockMode_OBJECT_LOCK_MODE_COMPLIANCE:
		out.DefaultMode = "COMPLIANCE"
	}
	return out
}

func versioningToProto(v admindomain.BucketVersioning) *pb.BucketVersioning {
	return &pb.BucketVersioning{Enabled: v.Enabled, KeepDeletesForever: v.KeepDeletesForever}
}

func versioningFromProto(p *pb.BucketVersioning) admindomain.BucketVersioning {
	if p == nil {
		return admindomain.BucketVersioning{}
	}
	return admindomain.BucketVersioning{Enabled: p.GetEnabled(), KeepDeletesForever: p.GetKeepDeletesForever()}
}

func replicationToProto(r admindomain.BucketReplication) *pb.BucketReplication {
	return &pb.BucketReplication{
		Enabled:           r.Enabled,
		DestinationBucket: r.DestinationBucket,
		Filter:            r.Filter,
	}
}

func replicationFromProto(p *pb.BucketReplication) admindomain.BucketReplication {
	if p == nil {
		return admindomain.BucketReplication{}
	}
	return admindomain.BucketReplication{
		Enabled:           p.GetEnabled(),
		DestinationBucket: p.GetDestinationBucket(),
		Filter:            p.GetFilter(),
	}
}

// ─── Audit / Quota / EventSubscription ↔ proto ──────────────────────────────

func auditEntryToProto(e *admindomain.AuditEntry) *pb.AuditLogEntry {
	if e == nil {
		return nil
	}
	return &pb.AuditLogEntry{
		EntryId:       e.EntryID.String(),
		At:            tsProto(e.At),
		ActorSubject:  e.ActorSubject,
		ActorTenantId: uuidStrEmpty(e.ActorTenantID.String()),
		ActorAudience: e.ActorAudience,
		Action:        e.Action,
		ResourceName:  e.ResourceName,
		RequestId:     e.RequestID,
		SourceIp:      e.SourceIP,
		BeforeJson:    e.BeforeJSON,
		AfterJson:     e.AfterJSON,
		ErrorMessage:  e.ErrorMessage,
		// CapabilityID is uuid.Nil for JWT/api-token paths;
		// uuidStrEmpty maps the zero UUID to "" so the wire payload
		// is tight and the UI can render with a falsy check.
		CapabilityId: uuidStrEmpty(e.CapabilityID.String()),
	}
}

func quotaToProto(q *admindomain.Quota) *pb.Quota {
	if q == nil {
		return nil
	}
	var name string
	switch {
	case q.TenantID.String() != "00000000-0000-0000-0000-000000000000":
		name = fmt.Sprintf("tenants/%s/quota", q.TenantID)
	case q.BackendID != "" && q.BucketName != "":
		name = fmt.Sprintf("storageBackends/%s/buckets/%s/quota", q.BackendID, q.BucketName)
	}
	return &pb.Quota{
		Name:             name,
		MaxTotalBytes:    q.MaxTotalBytes,
		MaxObjectCount:   q.MaxObjectCount,
		MaxBytesPerDay:   q.MaxBytesPerDay,
		MaxObjectsPerDay: q.MaxObjectsPerDay,
		Usage: &pb.QuotaUsage{
			TotalBytes:   q.UsageTotalBytes,
			ObjectCount:  q.UsageObjectCount,
			BytesToday:   q.UsageBytesToday,
			ObjectsToday: q.UsageObjectsToday,
			LastResetAt:  tsPtrProto(q.LastResetAt),
		},
		ResourceVersion: resourceVersion(q.ResourceVersion),
		UpdatedAt:       tsProto(q.UpdatedAt),
	}
}

func eventSubToProto(s *admindomain.EventSubscription) *pb.EventSubscription {
	if s == nil {
		return nil
	}
	out := &pb.EventSubscription{
		Name:            fmt.Sprintf("tenants/%s/eventSubscriptions/%s", s.TenantID, s.SubscriptionID),
		TenantId:        s.TenantID.String(),
		Filter:          s.CELFilter,
		Disabled:        s.Disabled,
		ResourceVersion: resourceVersion(s.ResourceVersion),
		CreatedAt:       tsProto(s.CreatedAt),
		UpdatedAt:       tsProto(s.UpdatedAt),
	}
	out.Sink = sinkFromConfig(s.SinkKind, s.SinkConfig)
	return out
}

func sinkFromConfig(kind string, cfg []byte) *pb.EventSink {
	out := &pb.EventSink{}
	switch kind {
	case "http":
		var v pb.HttpSink
		_ = json.Unmarshal(cfg, &v)
		out.Target = &pb.EventSink_Http{Http: &v}
	case "kafka":
		var v pb.KafkaSink
		_ = json.Unmarshal(cfg, &v)
		out.Target = &pb.EventSink_Kafka{Kafka: &v}
	case "sqs":
		var v pb.SqsSink
		_ = json.Unmarshal(cfg, &v)
		out.Target = &pb.EventSink_Sqs{Sqs: &v}
	case "nats":
		var v pb.NatsSink
		_ = json.Unmarshal(cfg, &v)
		out.Target = &pb.EventSink_Nats{Nats: &v}
	case "rabbitmq":
		var v pb.RabbitMqSink
		_ = json.Unmarshal(cfg, &v)
		out.Target = &pb.EventSink_Rabbitmq{Rabbitmq: &v}
	}
	return out
}

func sinkToConfig(sink *pb.EventSink) (kind string, cfg []byte) {
	if sink == nil {
		return "", nil
	}
	switch t := sink.GetTarget().(type) {
	case *pb.EventSink_Http:
		b, _ := json.Marshal(t.Http)
		return "http", b
	case *pb.EventSink_Kafka:
		b, _ := json.Marshal(t.Kafka)
		return "kafka", b
	case *pb.EventSink_Sqs:
		b, _ := json.Marshal(t.Sqs)
		return "sqs", b
	case *pb.EventSink_Nats:
		b, _ := json.Marshal(t.Nats)
		return "nats", b
	case *pb.EventSink_Rabbitmq:
		b, _ := json.Marshal(t.Rabbitmq)
		return "rabbitmq", b
	}
	return "", nil
}

// ─── Resource-name helpers ──────────────────────────────────────────────────

func backendIDFromName(name string) (string, error) {
	const prefix = "storageBackends/"
	if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
		return "", fmt.Errorf("invalid backend name %q", name)
	}
	rest := name[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return "", fmt.Errorf("invalid backend name %q (has child segment)", name)
	}
	return rest, nil
}

// bucketNameParts parses "storageBackends/{backend}/buckets/{bucket}".
func bucketNameParts(name string) (backend, bucket string, err error) {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "storageBackends" || parts[2] != "buckets" {
		return "", "", fmt.Errorf("invalid bucket name %q", name)
	}
	return parts[1], parts[3], nil
}

func tenantIDFromName(name string) (string, error) {
	const prefix = "tenants/"
	if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
		return "", fmt.Errorf("invalid tenant name %q", name)
	}
	rest := name[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i], nil
	}
	return rest, nil
}

func subscriptionIDFromName(name string) (string, error) {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "tenants" || parts[2] != "eventSubscriptions" {
		return "", fmt.Errorf("invalid subscription name %q", name)
	}
	return parts[3], nil
}
