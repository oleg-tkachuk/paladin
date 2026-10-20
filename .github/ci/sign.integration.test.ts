// sign.ts against a throwaway registry.
//
// Copies a published multi-arch Paladin image — with BuildKit's attestation
// manifests in its index, like every image release.yaml pushes — into a local
// registry, signs it with a fresh key pair, and checks what came out:
//
//   - an SPDX SBOM per platform image, and none for the attestation manifests;
//   - signatures and SBOM attestations that verify with the signing key;
//   - and do NOT verify with another key, so the check above is not vacuous;
//   - the same for a Helm chart pushed to the registry.
//
// Keyless signing needs a CI OIDC token, so release.yaml's own verify step is
// the only place that path runs; everything else is the same code.
//
// Skipped unless PALADIN_SIGN_TEST is set: it needs docker (buildx), cosign,
// syft, crane, helm and network to ghcr.io. Runs as
// `task -t Taskfile.dev.yaml verify:release-signing`.

import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { execute } from "./lib/exec.ts";
import { SBOM_SUFFIX, SBOM_TYPE, signChart, signImage, signingMode } from "./sign.ts";

// SIGN_TEST_ENV turns the test on.
export const SIGN_TEST_ENV = "PALADIN_SIGN_TEST";
// A released image: multi-arch, with attestation manifests. Pinned by tag; the
// test reads its structure, not its contents.
const SOURCE_IMAGE = "ghcr.io/oleg-tkachuk/paladin-core:4.11.5";
const PLATFORMS_WANT = ["linux-amd64", "linux-arm64"];
const REGISTRY_IMAGE = "registry:3";
const REGISTRY_PORT = process.env.PALADIN_SIGN_TEST_PORT ?? "5055";
const TEST_REPO = "paladin-sign-test";
const SPDX_PREFIX = "SPDX-";
// A chart of helm's own scaffold, pushed under the path the release library
// uses; the test reads its signature, not its contents.
const TEST_CHART = "paladin-sign-test-chart";
const TEST_CHART_VERSION = "0.0.1";
const CHART_PATH = "charts";
const REGISTRY_READY_ATTEMPTS = 30;
const REGISTRY_READY_PAUSE_MS = 1000;

// fails reports whether the command fails.
const fails = (file: string, args: readonly string[]) => execute(file, args).then(() => false, () => true);

test("an image and a chart signed, an SBOM per platform, verifying with their key only", { skip: process.env[SIGN_TEST_ENV] === undefined }, async () => {
  const work = mkdtempSync(join(tmpdir(), "paladin-sign-test-"));
  const registry = (await execute("docker", ["run", "-d", "--rm", "-p", `127.0.0.1:${REGISTRY_PORT}:5000`, REGISTRY_IMAGE])).trim();
  try {
    const host = `localhost:${REGISTRY_PORT}`;
    for (let i = 0; i < REGISTRY_READY_ATTEMPTS && (await fails("crane", ["catalog", host])); i++) {
      await new Promise((resolve) => setTimeout(resolve, REGISTRY_READY_PAUSE_MS));
    }
    const image = `${host}/${TEST_REPO}`;
    await execute("crane", ["copy", SOURCE_IMAGE, `${image}:test`]);
    const digest = (await execute("crane", ["digest", `${image}:test`])).trim();
    const env = { ...process.env, COSIGN_PASSWORD: "" };
    for (const prefix of ["signer", "other"]) {
      await execute("cosign", ["generate-key-pair", "--output-key-prefix", prefix], { cwd: work, env });
    }
    process.env.COSIGN_PASSWORD = "";
    const mode = signingMode({ COSIGN_KEY: join(work, "signer.key"), COSIGN_PUB: join(work, "signer.pub") });
    const other = ["--key", join(work, "other.pub"), "--insecure-ignore-tlog=true"];

    const sbomDir = join(work, "sbom");
    await signImage(image, digest, sbomDir, mode);
    const got = readdirSync(sbomDir)
      .filter((f) => f.endsWith(SBOM_SUFFIX))
      .map((f) => f.slice(`${TEST_REPO}-`.length, -SBOM_SUFFIX.length))
      .sort();
    assert.deepEqual(got, PLATFORMS_WANT);
    for (const f of readdirSync(sbomDir)) {
      assert.ok((JSON.parse(readFileSync(join(sbomDir, f), "utf8")) as { spdxVersion: string }).spdxVersion.startsWith(SPDX_PREFIX), f);
    }

    assert.ok(await fails("cosign", ["verify", ...other, `${image}@${digest}`]), "the index verifies with a key that did not sign it");
    const index = JSON.parse(await execute("crane", ["manifest", `${image}@${digest}`])) as { manifests: { digest: string; platform: { os: string } }[] };
    const pdigest = index.manifests.find((m) => m.platform.os === "linux")?.digest;
    assert.ok(await fails("cosign", ["verify-attestation", ...other, "--type", SBOM_TYPE, `${image}@${pdigest}`]), "an SBOM attestation verifies with a key that did not sign it");

    await execute("helm", ["create", TEST_CHART], { cwd: work });
    await execute("helm", ["package", TEST_CHART, "--version", TEST_CHART_VERSION], { cwd: work });
    await execute("helm", ["push", `${TEST_CHART}-${TEST_CHART_VERSION}.tgz`, `oci://${host}/${CHART_PATH}`, "--plain-http"], { cwd: work });
    const chart = `${host}/${CHART_PATH}/${TEST_CHART}:${TEST_CHART_VERSION}`;
    await signChart(chart, mode);
    const chartRef = `${host}/${CHART_PATH}/${TEST_CHART}@${(await execute("crane", ["digest", chart])).trim()}`;
    assert.ok(!(await fails("cosign", ["verify", ...mode.verify, chartRef])), "the chart does not verify with its signing key");
    assert.ok(await fails("cosign", ["verify", ...other, chartRef]), "the chart verifies with a key that did not sign it");
  } finally {
    await execute("docker", ["rm", "-f", registry]).catch(() => undefined);
    rmSync(work, { recursive: true, force: true });
  }
});
