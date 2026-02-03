# Paladin API Documentation

This document provides detailed information about the REST and gRPC APIs exposed by the Paladin service.

## REST API

**Base URL**: `/v1`

The REST API is driven by an [OpenAPI 3.0 specification](./api/openapi.yaml). It is built using [Gin](https://gin-gonic.com/) and provides endpoints for managing objects and multipart uploads. It uses standard HTTP status codes and JSON for request/response bodies.

**Contract-First Development**:
The API follows a contract-first approach. Go types, server interfaces, and request validation are automatically generated from the OpenAPI spec. Documentation below describes the current contract, but the `openapi.yaml` file remains the absolute source of truth.

### Authentication & Multi-Tenancy

The service enforces tenant isolation. Tenant identity is derived from the authentication context.

- **Production**: Typically derived from JWT claims (Bearer token).
- **Trusted Gateway**: If configured (`security.trust_tenant_id_from_request: true`), the service respects the `X-Tenant-ID` header.

**Strict Enforcement**:

- The service strictly enforces tenant isolation. Every request must have a valid tenant context.
- If `security.trust_tenant_id_from_request` is enabled, the `X-Tenant-ID` header is **required**.
- If disabled, the service expects authentication via other means (e.g., JWT) that populates the tenant context.
- **Requests without a valid tenant context will be rejected with `401 Unauthorized`.**

### Transport Security

All API endpoints can be served over **HTTPS** with full TLS support (configurable via server settings). In production environments, it is recommended to enable TLS to ensure secure communication.

### Rate Limiting

The API implements a token-bucket rate limiter per tenant.

- **Limit**: 10 requests per second (default).
- **Burst**: 20 requests (default).

Exceeding the limit results in `429 Too Many Requests`.

### Common Error Response

All API endpoints may return the following error structure in case of 4xx or 5xx status codes:

```json
{
  "error": {
    "code": "string",
    "message": "string",
    "details": "string",
    "request_id": "string",
    "trace_id": "string"
  }
}
```

**Common Error Codes:**

| Code | HTTP Status | Description |
| :--- | :--- | :--- |
| `bad_request` | 400 | Malformed input |
| `validation_failed` | 400 | Logic validation failed |
| `unauthorized` | 401 | Missing/Invalid credentials |
| `forbidden` | 403 | Valid credentials, incomplete rights |
| `not_found` | 404 | Resource does not exist |
| `conflict` | 409 | Conflict with current state |
| `too_large` | 413 | Payload or object size exceeded |
| `too_many_requests` | 429 | Rate limit exceeded |
| `internal` | 500 | Unhandled exception/bug |

---

### Object Management

#### Create Object

Initiates a single-object upload session. Returns a presigned URL that the client should use to `PUT` the file content directly to storage.

- **Method**: `POST`
- **Endpoint**: `/objects`
- **Success Code**: `200 OK`
- **Error Codes**: `400 Bad Request`

**Request Body** (`application/json`):

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `content_type` | string | **Yes** | MIME type of the object (e.g., `image/jpeg`). Must be allowed by server policy. |
| `size_bytes` | int64 | **Yes** | Total size of the object in bytes. Must be > 0 and not exceed server limits. |
| `labels` | map[string]string | No | Optional key-value tags to attach to the object. |
| `external_ref` | string | No | Optional external reference ID (must be unique per tenant if provided). |

**Response Body**:

| Field | Type | Description |
| :--- | :--- | :--- |
| `object_id` | string (UUID) | Unique identifier for the created object. |
| `object_key` | string | Internal storage key (typically `tenant/uuid`). |
| `upload_url` | string | Presigned URL for the `PUT` request. |
| `method` | string | HTTP method to use for upload (always `PUT`). |
| `headers` | map[string]string | Optional headers to include in the upload request (e.g., `Content-Type`). |
| `expires_at` | string (ISO8601) | Timestamp when the presigned URL expires. |

**Example**:

```bash
curl -X POST http://localhost:8080/v1/objects \
  -H "Content-Type: application/json" \
  -d '{"content_type": "image/png", "size_bytes": 1024, "labels": {"type": "invoice"}}'
```

> **Note**: If Server-Side Encryption (SSE) is enabled, the `headers` field in the response will contain the required encryption headers (e.g., `x-amz-server-side-encryption`). These headers **must** be included in your `PUT` request to S3, or the upload will fail.

#### Get Object

Retrieves metadata for an existing object and generates a presigned URL for downloading.

- **Method**: `GET`
- **Endpoint**: `/objects/:id`
- **Path Parameters**:
  - `id`: Object UUID
- **Success Code**: `200 OK`
- **Error Codes**: `400 Bad Request`, `404 Not Found`

**Response Body**:

| Field | Type | Description |
| :--- | :--- | :--- |
| `object_id` | string (UUID) | Unique identifier of the object. |
| `object_key` | string | Internal storage key. |
| `bucket` | string | Name of the S3 bucket where the object is stored. |
| `content_type` | string | MIME type of the object. |
| `size_bytes` | int64 | Size of the object in bytes. |
| `status` | string | Current state: `pending`, `active`, or `deleted`. |
| `download_url` | string | Presigned URL to download the file. |
| `expires_at` | string (ISO8601) | Timestamp when the download URL expires. |
| `labels` | map[string]string | Custom key-value tags. |
| `external_ref` | string | External reference ID. |

#### Get Object Metadata

Retrieves metadata for an existing object **without** generating a download URL. Useful for checking status or headers.

- **Method**: `GET`
- **Endpoint**: `/objects/:id/meta`
- **Path Parameters**:
  - `id`: Object UUID
- **Success Code**: `200 OK`
- **Error Codes**: `400 Bad Request`, `404 Not Found`

**Response Body**:

| Field | Type | Description |
| :--- | :--- | :--- |
| `object_id` | string (UUID) | Unique identifier of the object. |
| `object_key` | string | Internal storage key. |
| `bucket` | string | Name of the S3 bucket where the object is stored. |
| `content_type` | string | MIME type of the object. |
| `size_bytes` | int64 | Size of the object in bytes. |
| `status` | string | Current state: `pending`, `active`, or `deleted`. |
| `labels` | map[string]string | Custom key-value tags. |
| `external_ref` | string | External reference ID. |
| `expires_at` | string (ISO8601) | Optional expiration timestamp (if set). |

#### Complete Object

Marks an upload as complete and the object as `active`. This tells the system that the client has successfully uploaded the file to the presigned URL.

- **Method**: `POST`
- **Endpoint**: `/objects/:id/complete`
- **Path Parameters**:
  - `id`: Object UUID
- **Success Code**: `200 OK`
- **Error Codes**: `400 Bad Request`, `500 Internal Server Error`

**Response Body**:

| Field | Type | Description |
| :--- | :--- | :--- |
| `status` | string | The new status of the object (typically `active`). |

#### Delete Object

Soft-deletes an object. The object data remains in storage until cleaned up by a lifecycle policy, but the object is marked `deleted` in the database and is no longer accessible via the API.

- **Method**: `DELETE`
- **Endpoint**: `/objects/:id`
- **Path Parameters**:
  - `id`: Object UUID
- **Success Code**: `200 OK`
- **Error Codes**: `400 Bad Request`, `500 Internal Server Error`

**Response Body**:

| Field | Type | Description |
| :--- | :--- | :--- |
| `status` | string | The new status of the object (`deleted`). |

---

### Multipart Uploads

#### Initiate Multipart Upload

Starts a multipart upload session. This is required for large files or when the size is unknown (though size is currently required by policy).

- **Method**: `POST`
- **Endpoint**: `/multipart`
- **Success Code**: `200 OK`
- **Error Codes**: `400 Bad Request`

**Request Body** (`application/json`):

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `content_type` | string | **Yes** | MIME type of the file. |
| `size_bytes` | int64 | **Yes** | Total size of the file. Used to calculate part sizing. |
| `labels` | map[string]string | No | Optional key-value tags. |
| `external_ref` | string | No | Optional external reference ID. |

**Response Body**:

| Field | Type | Description |
| :--- | :--- | :--- |
| `object_id` | string (UUID) | Unique identifier for the object. |
| `object_key` | string | Internal storage key. |
| `upload_id` | string | S3 multipart upload ID. Service-generated. |
| `part_size` | int64 | Recommended size for each part (e.g., 8MB). |
| `expires_at` | string (ISO8601) | Timestamp when this upload session expires. |

#### Sign Part

Generates a presigned URL for uploading a specific part number of a multipart upload.

- **Method**: `POST`
- **Endpoint**: `/multipart/:upload_id/parts/:part_number/sign`
- **Path Parameters**:
  - `upload_id`: The S3 multipart upload ID returned by `InitiateMultipart`.
  - `part_number`: The sequential number of the part (1-indexed).
- **Success Code**: `200 OK`
- **Error Codes**: `400 Bad Request`, `404 Not Found` (if upload session invalid)

**Response Body**:

| Field | Type | Description |
| :--- | :--- | :--- |
| `upload_url` | string | Presigned URL for `PUT`ing this specific part. |
| `method` | string | HTTP method (always `PUT`). |
| `expires_at` | string (ISO8601) | Expiration for this specific part's URL. |

#### Complete Multipart Upload

Finalizes a multipart upload. Requires a list of all uploaded parts and their ETags (returned by S3 in the response headers of each part upload).

- **Method**: `POST`
- **Endpoint**: `/multipart/:upload_id/complete`
- **Path Parameters**:
  - `upload_id`: The S3 multipart upload ID.
- **Success Code**: `200 OK`
- **Error Codes**: `400 Bad Request`

**Request Body** (`application/json`):

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `parts` | array | **Yes** | List of part information. |
| `parts[].part_number` | int32 | **Yes** | Part number (1-based). |
| `parts[].etag` | string | **Yes** | The ETag header value returned by S3 for this part. |

**Response Body**:

| Field | Type | Description |
| :--- | :--- | :--- |
| `object_id` | string (UUID) | The ID of the completed object. |
| `status` | string | New status (`active`). |

#### Abort Multipart Upload

Cancels a multipart upload session and instructs the storage backend to relinquish resources.

- **Method**: `POST`
- **Endpoint**: `/multipart/:upload_id/abort`
- **Path Parameters**:
  - `upload_id`: The S3 multipart upload ID.
- **Success Code**: `200 OK`
- **Error Codes**: `400 Bad Request`

**Response Body**:

| Field | Type | Description |
| :--- | :--- | :--- |
| `status` | string | New status (`aborted`). |

---

## gRPC API

**Service**: `Paladin`
**Package**: `paladin.v1`

The gRPC API acts as a mirror to the REST API. Clients should use the Generated Go Client (`internal/api/grpc/grpcapi`) to interact with it.

### Common Types

- **`int64 expires_at_unix`**: Timestamps in gRPC responses are Unix epoch seconds.

### Methods

#### `CreateObject`

Initiates a single object upload.

- **Request**: `CreateObjectRequest`
  - `tenant_id` (string): Tenant identifier.
  - `content_type` (string): MIME type.
  - `size_bytes` (int64): Object size.
  - `labels` (map<string, string>): Optional tags.
  - `external_ref` (string): Optional external reference.
- **Response**: `CreateObjectResponse`
  - `object_id` (string): UUID.
  - `object_key` (string): Storage key.
  - `upload_url` (string): Presigned PUT URL.
  - `method` (string): "PUT".
  - `headers` (map<string, string>): Required headers.
  - `expires_at_unix` (int64): URL expiration time.

#### `GetObject`

Gets object metadata and download URL.

- **Request**: `GetObjectRequest`
  - `tenant_id` (string)
  - `object_id` (string)
- **Response**: `GetObjectResponse`
  - `object_id` (string)
  - `object_key` (string)
  - `bucket` (string)
  - `content_type` (string)
  - `size_bytes` (int64)
  - `status` (string)
  - `download_url` (string)
  - `expires_at_unix` (int64)
  - `labels` (map<string, string>)
  - `external_ref` (string)

#### `GetObjectMeta`

Gets object metadata without download URL.

- **Request**: `GetObjectRequest`
  - `tenant_id` (string)
  - `object_id` (string)
- **Response**: `GetObjectMetaResponse`
  - `object_id` (string)
  - `object_key` (string)
  - `bucket` (string)
  - `content_type` (string)
  - `size_bytes` (int64)
  - `status` (string)
  - `expires_at_unix` (int64)
  - `labels` (map<string, string>)
  - `external_ref` (string)

#### `CompleteObject`

Finalizes an object.

- **Request**: `CompleteObjectRequest`
  - `tenant_id` (string)
  - `object_id` (string)
- **Response**: `CompleteObjectResponse`
  - `status` (string)

#### `DeleteObject`

Soft deletes an object.

- **Request**: `DeleteObjectRequest`
  - `tenant_id` (string)
  - `object_id` (string)
- **Response**: `DeleteObjectResponse`
  - `status` (string)

#### `InitiateMultipart`

Starts a multipart session.

- **Request**: `InitiateMultipartRequest`
  - `tenant_id` (string)
  - `content_type` (string)
  - `size_bytes` (int64)
  - `labels` (map<string, string>)
  - `external_ref` (string)
- **Response**: `InitiateMultipartResponse`
  - `object_id` (string)
  - `object_key` (string)
  - `upload_id` (string)
  - `part_size` (int64)
  - `expires_at_unix` (int64)

#### `SignPart`

Signs a single part.

- **Request**: `SignPartRequest`
  - `tenant_id` (string)
  - `upload_id` (string)
  - `part_number` (int32)
- **Response**: `SignPartResponse`
  - `upload_url` (string)
  - `method` (string)
  - `expires_at_unix` (int64)

#### `CompleteMultipart`

Completes a multipart session.

- **Request**: `CompleteMultipartRequest`
  - `tenant_id` (string)
  - `upload_id` (string)
  - `parts` (repeated `CompleteMultipartPart`)
    - `part_number` (int32)
    - `etag` (string)
- **Response**: `CompleteMultipartResponse`
  - `object_id` (string)
  - `status` (string)

#### `AbortMultipart`

Cancels a session.

- **Request**: `AbortMultipartRequest`
  - `tenant_id` (string)
  - `upload_id` (string)
- **Response**: `AbortMultipartResponse`
  - `status` (string)
