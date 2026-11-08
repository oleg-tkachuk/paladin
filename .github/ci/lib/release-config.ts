// The release streams .github/release/release.config.cjs defines — the file
// semantic-release cuts the tags with — read rather than restated.

import { createRequire } from "node:module";

import type { Documentation } from "../../release/paths-analyzer.d.mts";

// Stream is one release stream: its tag format, "<prefix>${version}", the
// format of the Go module tag cut beside it at the same version, if any, and
// the paths its analyzer reads commits by.
export interface Stream {
  tagFormat: string;
  goModuleTagFormat?: string;
  // Formats the stream's tags had before tagFormat: still read as its own —
  // the upgrade guide names releases by them — and never cut.
  legacyTagFormats?: string[];
  analyzer: { include?: string[]; exclude?: string[]; documentation?: Documentation };
}

// The streams by name: the product's and the SDKs'.
export const PRODUCT = "product";
export const SDK = "sdk";

// VERSION_PLACEHOLDER is where a tag format puts the version.
const VERSION_PLACEHOLDER = "${version}";
const VERSION = /^\d+\.\d+\.\d+$/;

// streams is every stream, in the order the config lists them.
export function streams(): Record<string, Stream> {
  const require = createRequire(import.meta.url);
  return (require("../../release/release.config.cjs") as { streams: Record<string, Stream> }).streams;
}

// tagPrefix is what the tags semantic-release cuts for a stream start with.
export function tagPrefix(stream: Stream): string {
  return stream.tagFormat.replace(VERSION_PLACEHOLDER, "");
}

// goModulePrefix is what a stream's Go module tags start with, or undefined
// when it cuts none.
export function goModulePrefix(stream: Stream): string | undefined {
  return stream.goModuleTagFormat?.replace(VERSION_PLACEHOLDER, "");
}

// goModuleTag is the Go module tag of the release tag cut, or undefined when
// its stream cuts none.
export function goModuleTag(tag: string, stream: Stream): string | undefined {
  const prefix = goModulePrefix(stream);
  return prefix === undefined ? undefined : prefix + tag.slice(tagPrefix(stream).length);
}

// withGoModuleTags is tags with each release's Go module tag after it, for
// the streams that cut one: the names a release goes by.
export function withGoModuleTags(tags: readonly string[], all: Record<string, Stream>): string[] {
  return tags.flatMap((tag) => {
    const name = streamOf(tag, all);
    const stream = name === undefined ? undefined : all[name];
    const module = stream === undefined || tag.startsWith(goModulePrefix(stream) ?? "\0") ? undefined : goModuleTag(tag, stream);
    return module === undefined ? [tag] : [tag, module];
  });
}

// isTagOf reports whether tag is one of stream's: a version after its own
// prefix, its Go module's, or a prefix it had before.
function isTagOf(tag: string, stream: Stream): boolean {
  const legacy = (stream.legacyTagFormats ?? []).map((f) => f.replace(VERSION_PLACEHOLDER, ""));
  return [tagPrefix(stream), goModulePrefix(stream), ...legacy].some((p) => p !== undefined && tag.startsWith(p) && VERSION.test(tag.slice(p.length)));
}

// streamOf is the name of the stream tag is a release of, or undefined.
export function streamOf(tag: string, all: Record<string, Stream>): string | undefined {
  return Object.entries(all).find(([, s]) => isTagOf(tag, s))?.[0];
}

// sdkStreams is the name of every stream but the product's, in config order:
// the SDKs, each with a tag and a GitHub release of its own.
export function sdkStreams(all: Record<string, Stream>): string[] {
  return Object.keys(all).filter((name) => name !== PRODUCT);
}
