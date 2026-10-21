// The release streams .github/release/release.config.cjs defines — the file
// semantic-release cuts the tags with — read rather than restated.

import { createRequire } from "node:module";

import type { Documentation } from "../../release/paths-analyzer.d.mts";

// Stream is one release stream: its tag format, "<prefix>${version}", and the
// paths its analyzer reads commits by.
export interface Stream {
  tagFormat: string;
  analyzer: { include?: string[]; exclude?: string[]; documentation?: Documentation };
}

// The streams by name: the product's and each SDK's.
export const PRODUCT = "product";
export const GO_SDK = "go-sdk";
export const PYTHON_SDK = "python-sdk";

// VERSION_PLACEHOLDER is where a tag format puts the version.
const VERSION_PLACEHOLDER = "${version}";
const VERSION = /^\d+\.\d+\.\d+$/;

// streams is every stream, in the order the config lists them.
export function streams(): Record<string, Stream> {
  const require = createRequire(import.meta.url);
  return (require("../../release/release.config.cjs") as { streams: Record<string, Stream> }).streams;
}

// tagPrefix is what a stream's tags start with.
export function tagPrefix(stream: Stream): string {
  return stream.tagFormat.replace(VERSION_PLACEHOLDER, "");
}

// streamOf is the name of the stream tag is a release of, or undefined.
export function streamOf(tag: string, all: Record<string, Stream>): string | undefined {
  return Object.entries(all).find(([, s]) => tag.startsWith(tagPrefix(s)) && VERSION.test(tag.slice(tagPrefix(s).length)))?.[0];
}

// sdkStreams is the name of every stream but the product's, in config order:
// the SDKs, each with a tag and a GitHub release of its own.
export function sdkStreams(all: Record<string, Stream>): string[] {
  return Object.keys(all).filter((name) => name !== PRODUCT);
}
