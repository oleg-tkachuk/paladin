#!/usr/bin/env bash
# Live adversarial security probe against a running PALADIN cluster.
#
# Exercises the boundaries that Cedar delegates to the edge (cross-tenant
# isolation via assertJWTTenant, audience separation) plus the dedicated-tenant
# flow end to end. Mutation-style: crafts JWTs that spoof tenant/slug/audience
# and asserts each is REJECTED.
#
# Usage:
#   backend/tests/api/security-probe.sh            # auto port-forwards
#   API=https://host:port ADMIN=https://host:port backend/tests/api/security-probe.sh
#
# Exit 0 = all probes upheld the invariants; non-zero = a boundary leaked.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
GENJWT="$HERE/lib/gen-jwt.sh"
NS="${NS:-paladin}"
SECRET="${JWT_HMAC:-dev-secret-change-me-32-bytes-min}"
PASS=0 FAIL=0
PF_PIDS=()

cleanup() { for p in "${PF_PIDS[@]:-}"; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT

pf() { # local_port svc remote_port
  kubectl port-forward -n "$NS" "svc/$2" "$1:$3" >/dev/null 2>&1 &
  PF_PIDS+=("$!")
}

if [ -z "${API:-}" ]; then pf 18080 paladin-api 8080; API=https://localhost:18080; fi
if [ -z "${ADMIN:-}" ]; then pf 18090 paladin-admin 8090; ADMIN=https://localhost:18090; fi
sleep 3

# jwt <aud> <tenant_uuid> <sub> <roles_csv> [slug]
jwt() { JWT_AUD="$1" JWT_SLUG="${5:-}" "$GENJWT" "$2" "$3" "$4" "${5:-}"; }

# call <base> <rpc> <token> <json> -> prints HTTP body; sets LAST_CODE
call() {
  curl -sk -X POST "$1/$2" \
    -H "Authorization: Bearer $3" -H "Content-Type: application/json" \
    -H "Idempotency-Key: probe-$(date +%s%N)" -d "$4"
}

# expect_denied <label> <body>  — pass if the response carries an error code
expect_denied() {
  if printf '%s' "$2" | grep -qE '"code"[[:space:]]*:'; then
    echo "  PASS  $1"; PASS=$((PASS+1))
  else
    echo "  FAIL  $1 — request was NOT rejected: $(printf '%s' "$2" | head -c 160)"; FAIL=$((FAIL+1))
  fi
}
# expect_ok <label> <body>
expect_ok() {
  if printf '%s' "$2" | grep -qE '"code"[[:space:]]*:'; then
    echo "  FAIL  $1 — unexpected error: $(printf '%s' "$2" | head -c 160)"; FAIL=$((FAIL+1))
  else
    echo "  PASS  $1"; PASS=$((PASS+1))
  fi
}

echo "== setup: platform admin creates victim (dedicated) + attacker tenants =="
PADMIN=$(jwt paladin-admin 019f0fa2-5a22-74ab-8f29-04b2fcac3179 probe-admin platform.admin)
HX=$(date +%s)
VICT=$(call "$ADMIN" paladin.admin.v1.TenantService/CreateTenant "$PADMIN" \
  "{\"tenant\":{\"slug\":\"vict-$HX\",\"displayName\":\"vict-$HX\",\"storageLayout\":\"dedicated\"}}")
VTID=$(printf '%s' "$VICT" | sed -n 's/.*"tenantId":"\([^"]*\)".*/\1/p')
ATTK=$(call "$ADMIN" paladin.admin.v1.TenantService/CreateTenant "$PADMIN" \
  "{\"tenant\":{\"slug\":\"attk-$HX\",\"displayName\":\"attk-$HX\"}}")
ATID=$(printf '%s' "$ATTK" | sed -n 's/.*"tenantId":"\([^"]*\)".*/\1/p')
[ -n "$VTID" ] && [ -n "$ATID" ] || { echo "setup failed (victim=$VTID attacker=$ATID)"; echo "$VICT"; exit 2; }
echo "  victim=$VTID  attacker=$ATID"

# Create the objectKey, then poll UploadObject until it succeeds — the
# provision gate returns FailedPrecondition on the dedicated bucket until the
# reconciler creates it, so the early attempts double as a live gate check.
OKR=$(call "$ADMIN" paladin.admin.v1.ObjectKeyService/CreateObjectKey "$PADMIN" \
  "{\"parent\":\"tenants/$VTID\",\"objectKey\":\"docs\",\"objectKeyResource\":{}}")
printf '%s' "$OKR" | grep -q '"objectKey":"docs"' || { echo "setup: CreateObjectKey failed: $(printf '%s' "$OKR" | head -c 200)"; exit 2; }
# Setup upload runs as the VICTIM itself (its own data-plane token): the data
# plane resolves the object_key under the caller's tenant, so a platform-admin
# acting on the victim's namespace would look under the wrong tenant.
PADMIN_DATA=$(jwt paladin-data "$VTID" victim-setup tenant.admin "vict-$HX")
VOID="" GATED=0
for i in $(seq 1 20); do
  UP=$(call "$API" paladin.data.v1.ObjectService/UploadObject "$PADMIN_DATA" \
    "{\"parent\":\"tenants/$VTID/objectKeys/docs\",\"key\":\"secret.txt\",\"contentType\":\"text/plain\",\"sizeHintBytes\":\"6\"}")
  VOID=$(printf '%s' "$UP" | sed -n 's/.*"objectId":"\([^"]*\)".*/\1/p')
  [ -n "$VOID" ] && break
  printf '%s' "$UP" | grep -q "provisioning" && GATED=1
  sleep 3
done
[ "$GATED" -eq 1 ] && { echo "  PASS  E0 upload gated while bucket provisioning"; PASS=$((PASS+1)); }
VNAME="tenants/$VTID/objectKeys/docs/objects/$VOID"
[ -n "$VOID" ] || { echo "setup: upload never succeeded"; echo "$UP"; exit 2; }

# Finish the upload so the object is AVAILABLE (E5 reads it): presign PUT →
# PUT bytes → CompleteObject.
RG=$(call "$API" paladin.data.v1.PresignService/RegenerateUploadUrl "$PADMIN_DATA" \
  "{\"name\":\"$VNAME\",\"contentType\":\"text/plain\"}")
PUTURL=$(printf '%s' "$RG" | sed -n 's/.*"url":"\([^"]*\)".*/\1/p' | head -1)
printf 'secret' | curl -sk -o /dev/null -X PUT "$PUTURL" -H "Content-Type: text/plain" --data-binary @- 2>/dev/null
call "$API" paladin.data.v1.ObjectService/CompleteObject "$PADMIN_DATA" "{\"name\":\"$VNAME\"}" >/dev/null
echo "  victim object=$VOID (available)"

echo "== probes =="

# E1: attacker MEMBER (own tenant, correct slug) reaches for the victim's object.
ATOK=$(jwt paladin-data "$ATID" attacker tenant.admin "attk-$HX")
R=$(call "$API" paladin.data.v1.PresignService/PresignDownload "$ATOK" "{\"name\":\"$VNAME\"}")
expect_denied "E1 cross-tenant object read (attacker→victim)" "$R"

# E2: attacker SPOOFS the victim's slug but keeps its own tenant UUID.
ATSPOOF=$(jwt paladin-data "$ATID" attacker tenant.admin "vict-$HX")
R=$(call "$API" paladin.data.v1.PresignService/PresignDownload "$ATSPOOF" "{\"name\":\"$VNAME\"}")
expect_denied "E2 slug-spoof cross-tenant read (attacker claims victim slug)" "$R"

# E3: audience confusion — a data-plane token on the admin plane, and vice versa.
DATATOK=$(jwt paladin-data "$VTID" x tenant.admin "vict-$HX")
R=$(call "$ADMIN" paladin.admin.v1.TenantService/GetTenant "$DATATOK" "{\"name\":\"tenants/$VTID\"}")
expect_denied "E3a paladin-data token on admin plane" "$R"
ADMINTOK=$(jwt paladin-admin "$VTID" x tenant.admin "vict-$HX")
R=$(call "$API" paladin.data.v1.PresignService/PresignDownload "$ADMINTOK" "{\"name\":\"$VNAME\"}")
expect_denied "E3b paladin-admin token on data plane" "$R"

# E4: forged signature (wrong HMAC secret) must be rejected.
FORGED=$(JWT_HMAC=not-the-real-secret-123456789012 jwt paladin-data "$VTID" x platform.admin "vict-$HX")
R=$(call "$API" paladin.data.v1.PresignService/PresignDownload "$FORGED" "{\"name\":\"$VNAME\"}")
expect_denied "E4 forged-signature token" "$R"

# E5: positive control — the legit victim member CAN read its own object.
VMEM=$(jwt paladin-data "$VTID" victim-member tenant.user "vict-$HX")
R=$(call "$API" paladin.data.v1.PresignService/PresignDownload "$VMEM" "{\"name\":\"$VNAME\"}")
expect_ok "E5 legit member reads own object (control)" "$R"

echo
echo "== result: $PASS passed, $FAIL failed =="
[ "$FAIL" -eq 0 ]
