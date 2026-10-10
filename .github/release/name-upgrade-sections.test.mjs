// name-upgrade-sections.test.mjs — which tags each Unreleased section of the
// upgrade guide is named with, on the streams release.config.cjs defines.
//
// A section named with the wrong tag sends the release notes' migration link
// to the wrong place, or to none: the SDK's Python 3.13 section was cut in the
// same run as product v26.1.1, and belongs to the SDK alone.
//
// Runs from `task -t Taskfile.dev.yaml verify-repo` after `npm ci` here.

import { createRequire } from "node:module";
import { nameSections, streamOf, tagsFor } from "./name-upgrade-sections.mjs";

const require = createRequire(import.meta.url);
const { streams } = require("./release.config.cjs");

const PRODUCT_TAG = "v27.0.0";
const SDK_TAG = "sdk/go/v0.60.0";
const BOTH = [SDK_TAG, PRODUCT_TAG];

const BACKEND = "backend/internal/api/handler.go";
const GO_SDK = "sdk/go/paladin/client.go";
const PYTHON_SDK = "sdk/python/pyproject.toml";
const UPGRADING = "docs/upgrading.md";

let failed = false;
const check = (name, got, want) => {
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    failed = true;
    console.log(`FAIL ${name}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
  }
};

// [case, files the section's commit changed, tags it is named with]
const cases = [
  ["the Python SDK and its notes", [PYTHON_SDK, UPGRADING], [SDK_TAG]],
  ["the backend and its notes", [BACKEND, UPGRADING], [PRODUCT_TAG]],
  ["the Go SDK, which the product compiles in", [GO_SDK, UPGRADING], [PRODUCT_TAG, SDK_TAG]],
  ["the notes alone read as the product's", [UPGRADING], [PRODUCT_TAG]],
];
for (const [name, files, want] of cases) check(name, tagsFor(files, BOTH, streams), want);

check("a run that cut no SDK tag", tagsFor([PYTHON_SDK, UPGRADING], [PRODUCT_TAG], streams), []);
check("not a release tag", tagsFor([BACKEND], ["api/v1.2.0"], streams), []);
check("product tag's stream", streamOf(PRODUCT_TAG, streams), "product");
check("SDK tag's stream", streamOf(SDK_TAG, streams), "sdk");
check("a tag of no stream", streamOf("sdk/go/vnext", streams), undefined);

const GUIDE = [
  "# Upgrading",
  "",
  "## Unreleased — the SDK drops a call",
  "",
  "## Unreleased — the API and the SDK change together",
  "",
  "## Unreleased — not this run's",
  "",
  "## v26.0.0 — already named",
  "",
].join("\n");
const byHeading = {
  "## Unreleased — the SDK drops a call": [SDK_TAG],
  "## Unreleased — the API and the SDK change together": [PRODUCT_TAG, SDK_TAG],
};
check(
  "a guide with sections of both streams",
  nameSections(GUIDE, (line) => byHeading[line] ?? []),
  [
    "# Upgrading",
    "",
    `## ${SDK_TAG} — the SDK drops a call`,
    "",
    `## ${PRODUCT_TAG}, ${SDK_TAG} — the API and the SDK change together`,
    "",
    "## Unreleased — not this run's",
    "",
    "## v26.0.0 — already named",
    "",
  ].join("\n"),
);
const NOTHING_UNRELEASED = "# Upgrading\n\n## v26.0.0 — already named\n";
check("a guide with nothing unreleased", nameSections(NOTHING_UNRELEASED, () => BOTH), NOTHING_UNRELEASED);

if (failed) process.exit(1);
console.log("upgrade sections: each is named with the tags of the streams it belongs to");
