-- objects: metadata record for object lifecycle
CREATE TABLE IF NOT EXISTS objects (
  id              UUID PRIMARY KEY,
  tenant_id        TEXT NOT NULL,
  object_key       TEXT NOT NULL,
  bucket           TEXT NOT NULL,
  content_type     TEXT NOT NULL,
  size_bytes       BIGINT NOT NULL,
  checksum_sha256  TEXT NULL,
  status           TEXT NOT NULL, -- pending|active|deleted
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at       TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_objects_tenant_created_at ON objects (tenant_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_objects_tenant_key ON objects (tenant_id, object_key);

-- multipart uploads (control-plane state)
CREATE TABLE IF NOT EXISTS multipart_uploads (
  id              UUID PRIMARY KEY,
  tenant_id        TEXT NOT NULL,
  object_id        UUID NOT NULL REFERENCES objects(id) ON DELETE CASCADE,
  upload_id        TEXT NOT NULL,
  bucket           TEXT NOT NULL,
  object_key       TEXT NOT NULL,
  content_type     TEXT NOT NULL,
  part_size_bytes  BIGINT NOT NULL,
  status           TEXT NOT NULL, -- initiated|completed|aborted|expired
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at       TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mpu_tenant_created_at ON multipart_uploads (tenant_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_mpu_tenant_upload ON multipart_uploads (tenant_id, upload_id);

-- multipart parts (optional tracking)
CREATE TABLE IF NOT EXISTS multipart_parts (
  multipart_id     UUID NOT NULL REFERENCES multipart_uploads(id) ON DELETE CASCADE,
  part_number      INT NOT NULL,
  etag             TEXT NULL,
  size_bytes       BIGINT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (multipart_id, part_number)
);

CREATE INDEX IF NOT EXISTS idx_mpp_multipart_id ON multipart_parts (multipart_id);
