// Sign a published image and attest an SBOM for each of its platforms, or
// sign a pushed Helm chart, and verify both before the release goes out.
//
// Usage: node sign.ts image <image> <index-digest> <sbom-dir>
//        node sign.ts chart <chart-reference>            e.g. ghcr.io/o/charts/c:1.2.3
//
// Everything is addressed by digest: a tag can be moved after it is signed, a
// digest cannot.
//
//   - The index and every platform manifest are signed (cosign --recursive),
//     so a pull by tag and a pull by platform digest both find a signature.
//   - Each platform gets its own SPDX SBOM. Syft run against an index scans
//     one platform only, so an SBOM attached to the index would describe an
//     image half the pullers never get. The SBOM is attested (signed in-toto)
//     to that platform's digest and written to <sbom-dir> for the release.
//   - A chart is signed with the same identity; its digest comes from crane
//     rather than from `helm push`, which reports it only as a line of text.
//   - Both are verified right after, against the identity they must carry. A
//     signature that does not verify fails the release here, not in a user's
//     admission controller.
//
// Signing mode, from the environment:
//
//   keyless (default)  Sigstore with the job's OIDC token; verification pins
//                      CERT_IDENTITY — this workflow at the ref it ran from —
//                      and CERT_OIDC_ISSUER.
//   COSIGN_KEY, COSIGN_PUB  a key pair and no transparency log, what
//                      sign.integration.test.ts uses against a throwaway
//                      registry.
//
// Every call to a public service is tried again (lib/retry.ts). Requires
// docker (buildx), cosign, syft and crane on PATH; SYFT may name syft's path.

import { mkdirSync } from "node:fs";
import { join } from "node:path";

import { info, summarize, table } from "./lib/actions.ts";
import { optionalEnv } from "./lib/env.ts";
import { execute } from "./lib/exec.ts";
import { runMain } from "./lib/main.ts";
import { retry } from "./lib/retry.ts";

// SBOM_TYPE is the predicate type cosign names an SPDX JSON document by,
// SBOM_FORMAT syft's name for it.
export const SBOM_TYPE = "spdxjson";
const SBOM_FORMAT = "spdx-json";
export const SBOM_SUFFIX = ".spdx.json";
// ATTESTATION_OS is the platform BuildKit gives its own attestations, which
// are not images and have nothing to scan.
const ATTESTATION_OS = "unknown";
// RELEASE_WORKFLOW is the workflow a keyless signature names.
export const RELEASE_WORKFLOW = ".github/workflows/release.yaml";

// Command is what the step asks for.
export const Command = { Image: "image", Chart: "chart" } as const;

// Mode is the cosign arguments signing and verifying take.
export interface Mode {
  sign: string[];
  verify: string[];
}

type Env = Record<string, string | undefined>;
const set = (env: Env, name: string) => (env[name] === undefined || env[name] === "" ? undefined : env[name]);

// certIdentity is the identity a keyless signature carries: CERT_IDENTITY,
// else this workflow at the ref the run is on.
export function certIdentity(env: Env): string | undefined {
  const explicit = set(env, "CERT_IDENTITY");
  if (explicit !== undefined) return explicit;
  const [server, repository, ref] = [set(env, "GITHUB_SERVER_URL"), set(env, "GITHUB_REPOSITORY"), set(env, "GITHUB_REF")];
  return server === undefined || repository === undefined || ref === undefined ? undefined : `${server}/${repository}/${RELEASE_WORKFLOW}@${ref}`;
}

// signingMode is the mode the environment selects.
export function signingMode(env: Env): Mode {
  const key = set(env, "COSIGN_KEY");
  if (key !== undefined) {
    const pub = set(env, "COSIGN_PUB");
    if (pub === undefined) throw new Error("COSIGN_PUB must be set with COSIGN_KEY");
    return { sign: ["--key", key, "--use-signing-config=false", "--tlog-upload=false"], verify: ["--key", pub, "--insecure-ignore-tlog=true"] };
  }
  const identity = certIdentity(env);
  const issuer = set(env, "CERT_OIDC_ISSUER");
  if (identity === undefined) throw new Error("CERT_IDENTITY must name the signing workflow");
  if (issuer === undefined) throw new Error("CERT_OIDC_ISSUER must name the token issuer");
  return { sign: [], verify: ["--certificate-identity", identity, "--certificate-oidc-issuer", issuer] };
}

// Platform is one image of an index: <os>-<arch>[-<variant>], and its digest.
export interface Platform {
  name: string;
  digest: string;
}

interface Manifest {
  manifests?: { digest: string; platform?: { os?: string; architecture?: string; variant?: string } }[];
}

// platforms are the images an index lists, as `docker buildx imagetools
// inspect --format '{{json .Manifest}}'` prints it, its attestations left out.
export function platforms(manifestJson: string): Platform[] {
  const manifest = JSON.parse(manifestJson) as Manifest;
  return (manifest.manifests ?? [])
    .filter((m) => m.platform?.os !== ATTESTATION_OS)
    .map((m) => ({
      name: [m.platform?.os, m.platform?.architecture, m.platform?.variant].filter((p) => p !== undefined && p !== "").join("-"),
      digest: m.digest,
    }));
}

// Run runs a command and returns its standard output.
export type Run = (file: string, args: readonly string[]) => Promise<string>;

// realRun runs it, tried again: every command here reaches a registry or
// Sigstore.
const realRun: Run = (file, args) => retry(`${file} ${args[0] ?? ""}`, () => execute(file, args));

// Tools names the binaries, so a test or the workflow can point at its own.
export interface Tools {
  docker: string;
  cosign: string;
  syft: string;
  crane: string;
}

export const TOOLS: Tools = { docker: "docker", cosign: "cosign", syft: "syft", crane: "crane" };

// signImage signs image@digest and its platforms, attests an SBOM to each,
// verifies all of it, and returns the SBOM files it wrote.
export async function signImage(image: string, digest: string, sbomDir: string, mode: Mode, run: Run = realRun, tools: Tools = TOOLS): Promise<string[]> {
  const ref = `${image}@${digest}`;
  const images = platforms(await run(tools.docker, ["buildx", "imagetools", "inspect", ref, "--format", "{{json .Manifest}}"]));
  if (images.length === 0) {
    throw new Error(`${ref} lists no platform images — refusing to sign an empty index`);
  }
  mkdirSync(sbomDir, { recursive: true });
  info(`>>> [sign] ${ref} and its platform manifests`);
  await run(tools.cosign, ["sign", "--yes", "--recursive", ...mode.sign, ref]);

  const name = image.split("/").at(-1) ?? image;
  const sboms: string[] = [];
  for (const p of images) {
    const pref = `${image}@${p.digest}`;
    const sbom = join(sbomDir, `${name}-${p.name}${SBOM_SUFFIX}`);
    info(`>>> [sbom] ${p.name}: ${pref}`);
    await run(tools.syft, ["scan", `registry:${pref}`, "--output", `${SBOM_FORMAT}=${sbom}`, "--quiet"]);
    await run(tools.cosign, ["attest", "--yes", ...mode.sign, "--type", SBOM_TYPE, "--predicate", sbom, pref]);
    sboms.push(sbom);
  }

  info(">>> [verify] signatures and SBOM attestations");
  await run(tools.cosign, ["verify", ...mode.verify, ref]);
  for (const p of images) {
    const pref = `${image}@${p.digest}`;
    await run(tools.cosign, ["verify", ...mode.verify, pref]);
    await run(tools.cosign, ["verify-attestation", ...mode.verify, "--type", SBOM_TYPE, pref]);
    info(`    ${p.name}: signed, SBOM attested`);
  }
  summarize(imageSummary(image, digest, images.map((p) => p.name)));
  return sboms;
}

// byDigest is chart, <repository>:<tag>, as <repository>@digest.
export function byDigest(chart: string, digest: string): string {
  const colon = chart.lastIndexOf(":");
  if (colon <= chart.lastIndexOf("/")) {
    throw new Error(`${chart} has no tag`);
  }
  return `${chart.slice(0, colon)}@${digest}`;
}

// signChart signs the chart by its digest and verifies it.
export async function signChart(chart: string, mode: Mode, run: Run = realRun, tools: Tools = TOOLS): Promise<void> {
  const digest = (await run(tools.crane, ["digest", chart])).trim();
  const ref = byDigest(chart, digest);
  info(`>>> [sign] ${chart} (${digest})`);
  await run(tools.cosign, ["sign", "--yes", ...mode.sign, ref]);
  await run(tools.cosign, ["verify", ...mode.verify, ref]);
  info("    signed and verified");
  summarize(chartSummary(chart, digest));
}

// What the summary says each published artifact is.
const SIGNED_HEADER = ["Published", "Reference", "Checked"];
const IMAGE_CHECKED = "signed; an SBOM attested to each platform; verified";
const CHART_CHECKED = "signed; verified";

// imageSummary is the summary page's row for a signed image: the index by
// its digest, and each platform it holds.
export function imageSummary(image: string, digest: string, platformNames: readonly string[]): string {
  return table(SIGNED_HEADER, [[`image (${platformNames.join(", ")})`, `\`${image}@${digest}\``, IMAGE_CHECKED]]);
}

// chartSummary is the summary page's row for a signed chart.
export function chartSummary(chart: string, digest: string): string {
  return table(SIGNED_HEADER, [["chart", `\`${chart}\` (\`${digest}\`)`, CHART_CHECKED]]);
}

async function main(): Promise<void> {
  const [command, ...args] = process.argv.slice(2);
  const mode = signingMode(process.env);
  const tools = { ...TOOLS, syft: optionalEnv("SYFT") ?? TOOLS.syft };
  if (command === Command.Image && args.length === 3) {
    const [image = "", digest = "", sbomDir = ""] = args;
    await signImage(image, digest, sbomDir, mode, realRun, tools);
  } else if (command === Command.Chart && args.length === 1) {
    await signChart(args[0] ?? "", mode, realRun, tools);
  } else {
    throw new Error(`usage: sign.ts ${Command.Image} <image> <index-digest> <sbom-dir> | ${Command.Chart} <chart-reference>`);
  }
}

if (import.meta.main) {
  await runMain(main);
}
