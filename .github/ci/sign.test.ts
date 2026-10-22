import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import { RELEASE_WORKFLOW, SBOM_TYPE, TOOLS, byDigest, certIdentity, chartSummary, imageSummary, platforms, signChart, signImage, signingMode, type Run } from "./sign.ts";

describe("signingMode", () => {
  test("a key pair, and no transparency log", () => {
    assert.deepEqual(signingMode({ COSIGN_KEY: "k", COSIGN_PUB: "p" }), {
      sign: ["--key", "k", "--use-signing-config=false", "--tlog-upload=false"],
      verify: ["--key", "p", "--insecure-ignore-tlog=true"],
    });
    assert.throws(() => signingMode({ COSIGN_KEY: "k" }), /COSIGN_PUB/);
  });
  test("keyless, verified against this workflow at its ref", () => {
    const env = { GITHUB_SERVER_URL: "https://github.com", GITHUB_REPOSITORY: "o/r", GITHUB_REF: "refs/heads/main", CERT_OIDC_ISSUER: "https://issuer" };
    assert.equal(certIdentity(env), `https://github.com/o/r/${RELEASE_WORKFLOW}@refs/heads/main`);
    assert.deepEqual(signingMode(env), { sign: [], verify: ["--certificate-identity", `https://github.com/o/r/${RELEASE_WORKFLOW}@refs/heads/main`, "--certificate-oidc-issuer", "https://issuer"] });
    assert.equal(certIdentity({ ...env, CERT_IDENTITY: "x" }), "x");
    assert.throws(() => signingMode({ CERT_OIDC_ISSUER: "i" }), /CERT_IDENTITY/);
    assert.throws(() => signingMode({ CERT_IDENTITY: "x" }), /CERT_OIDC_ISSUER/);
  });
});

const INDEX = JSON.stringify({
  manifests: [
    { digest: "sha256:a", platform: { os: "linux", architecture: "amd64" } },
    { digest: "sha256:b", platform: { os: "linux", architecture: "arm64", variant: "v8" } },
    { digest: "sha256:c", platform: { os: "unknown", architecture: "unknown" } },
  ],
});

test("an index's platform images, its attestations left out", () => {
  assert.deepEqual(platforms(INDEX), [
    { name: "linux-amd64", digest: "sha256:a" },
    { name: "linux-arm64-v8", digest: "sha256:b" },
  ]);
});

test("a chart is addressed by digest, a registry port kept", () => {
  assert.equal(byDigest("localhost:5055/charts/c:0.0.1", "sha256:d"), "localhost:5055/charts/c@sha256:d");
  assert.throws(() => byDigest("localhost:5055/charts/c", "sha256:d"), /has no tag/);
});

// recorder is a Run that logs each command and answers inspect and digest.
function recorder(index = INDEX) {
  const calls: string[] = [];
  const run: Run = async (file, args) => {
    calls.push([file, ...args].join(" "));
    if (args[0] === "buildx") return index;
    if (args[0] === "digest") return "sha256:chart\n";
    return "";
  };
  return { calls, run };
}

const mode = { sign: ["--key", "k"], verify: ["--key", "p"] };

test("an image: signed, an SBOM per platform attested, then all verified", async () => {
  const dir = mkdtempSync(join(tmpdir(), "paladin-ci-"));
  try {
    const { calls, run } = recorder();
    const sboms = await signImage("r/o/img", "sha256:i", dir, mode, run);
    assert.deepEqual(sboms, [join(dir, "img-linux-amd64.spdx.json"), join(dir, "img-linux-arm64-v8.spdx.json")]);
    const cosign = calls.filter((c) => c.startsWith(`${TOOLS.cosign} `));
    assert.deepEqual(cosign, [
      "cosign sign --yes --recursive --key k r/o/img@sha256:i",
      `cosign attest --yes --key k --type ${SBOM_TYPE} --predicate ${sboms[0]} r/o/img@sha256:a`,
      `cosign attest --yes --key k --type ${SBOM_TYPE} --predicate ${sboms[1]} r/o/img@sha256:b`,
      "cosign verify --key p r/o/img@sha256:i",
      "cosign verify --key p r/o/img@sha256:a",
      `cosign verify-attestation --key p --type ${SBOM_TYPE} r/o/img@sha256:a`,
      "cosign verify --key p r/o/img@sha256:b",
      `cosign verify-attestation --key p --type ${SBOM_TYPE} r/o/img@sha256:b`,
    ]);
    assert.ok(calls.includes(`syft scan registry:r/o/img@sha256:a --output spdx-json=${sboms[0]} --quiet`));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("an empty index is not signed", async () => {
  const { calls, run } = recorder(JSON.stringify({ manifests: [{ digest: "sha256:c", platform: { os: "unknown" } }] }));
  await assert.rejects(signImage("r/o/img", "sha256:i", "/nonexistent", mode, run), /lists no platform images/);
  assert.equal(calls.filter((c) => c.startsWith("cosign")).length, 0);
});

test("a chart: signed and verified by its digest", async () => {
  const { calls, run } = recorder();
  await signChart("r/o/charts/c:1.2.3", mode, run);
  assert.deepEqual(calls, ["crane digest r/o/charts/c:1.2.3", "cosign sign --yes --key k r/o/charts/c@sha256:chart", "cosign verify --key p r/o/charts/c@sha256:chart"]);
});

describe("the summary of what was signed", () => {
  test("names an image by its digest, and its platforms", () => {
    const row = imageSummary("ghcr.io/o/core", "sha256:abc", ["linux/amd64", "linux/arm64"]);
    assert.ok(row.includes("| image (linux/amd64, linux/arm64) | `ghcr.io/o/core@sha256:abc` |"), row);
    assert.ok(row.includes("SBOM"), row);
  });

  test("names a chart by its tag and its digest", () => {
    const row = chartSummary("ghcr.io/o/charts/core:40.0.0", "sha256:def");
    assert.ok(row.includes("| chart | `ghcr.io/o/charts/core:40.0.0` (`sha256:def`) |"), row);
  });
});
