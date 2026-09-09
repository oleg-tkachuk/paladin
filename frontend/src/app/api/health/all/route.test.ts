import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

// The aggregator names its upstreams by environment variable; the chart is
// what sets them. Nothing connected the two, and they drifted: PALADIN_INGEST_URL
// was read here and rendered by no template, so an operator who set
// `backend.urls.ingest` in their values got silence — the key was accepted,
// wired to nothing, and the aggregator quietly kept using its in-cluster
// default. It surfaced only when the chart gained a values schema and started
// refusing the key outright.
//
// Reading both files as text is deliberate. ROLES is not exported (a Next.js
// route module should export route handlers and little else), and the chart
// side is YAML either way — so the pairing is asserted where it actually
// lives, on both sides, rather than against a copy kept in a test.

const chartDir = join(__dirname, "../../../../../deploy/chart");
const deployment = readFileSync(
  join(chartDir, "templates/deployment.yaml"),
  "utf8",
);
const values = readFileSync(join(chartDir, "values.yaml"), "utf8");
const route = readFileSync(join(__dirname, "route.ts"), "utf8");

/** Env keys the aggregator reads, in declaration order. */
const readByAggregator = [...route.matchAll(/envKey:\s*"([^"]+)"/g)].map(
  (m) => m[1],
);

// Every TypeScript source under src/, read once — tests excluded. A test
// naming an env var in a comment (this file names two) would otherwise
// satisfy the "somebody reads it" check on its own.
const sources = (function read(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return read(p);
    if (!/\.tsx?$/.test(e.name) || /\.test\.tsx?$/.test(e.name)) return [];
    return [readFileSync(p, "utf8")];
  });
})(join(__dirname, "../../../../"));

/** PALADIN_*_URL names the chart renders into the container's env block. */
const setByChart = [
  ...deployment.matchAll(/- name:\s*(PALADIN_[A-Z_]*URL)/g),
].map((m) => m[1]);

describe("health aggregator ↔ console chart", () => {
  it("finds the role table and the chart's env block", () => {
    // Guards the two regexes above: a rename that makes them match nothing
    // would otherwise turn both assertions below into vacuous truths.
    expect(readByAggregator.length).toBeGreaterThanOrEqual(6);
    expect(setByChart.length).toBeGreaterThanOrEqual(6);
  });

  it("renders every env key the aggregator reads", () => {
    const missing = readByAggregator.filter((k) => !setByChart.includes(k));
    expect(
      missing,
      "read by /api/health/all, set by no chart template",
    ).toEqual([]);
  });

  it("has a reader in src/ for every PALADIN_*_URL the chart renders", () => {
    // The other direction, and deliberately wider than this route: an env var
    // nothing consumes is dead config that reads as a supported knob.
    // PALADIN_DATA_URL is the case that makes the wider scope necessary — the
    // RPC proxy reads it (src/lib/server/upstream.ts), not the aggregator.
    const unused = setByChart.filter(
      (k) => !sources.some((f) => f.includes(k)),
    );
    expect(unused, "rendered by the chart, read by nothing in src/").toEqual(
      [],
    );
  });

  it("backs each env key with a backend.urls entry in values.yaml", () => {
    const urls = values.slice(values.indexOf("backend:"));
    const declared = [...urls.matchAll(/^ {4}([a-z]+):\s*"/gm)].map(
      (m) => m[1],
    );
    expect(declared.length).toBeGreaterThanOrEqual(6);
    // data is the Connect data plane; the aggregator polls iam on the same
    // pod, so it is the one URL with no envKey of its own here.
    const expected = declared.filter((r) => r !== "data").sort();
    const fromEnv = readByAggregator
      .map((k) => k.replace(/^PALADIN_|_URL$/g, "").toLowerCase())
      .sort();
    expect(expected).toEqual(fromEnv);
  });
});
