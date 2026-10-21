// What marks a dev build's version, <x.y.z>-dev.<…>: the check that refuses
// to publish one as a release, and the cleanup that deletes only dev builds,
// both read it here.
export const DEV_MARK = "-dev.";

// isDevVersion reports whether version is a dev build's.
export function isDevVersion(version: string): boolean {
  return version.includes(DEV_MARK);
}

// RC is the prerelease identifier of a release candidate, <x.y.z>-rc.<n>.
export const RC = "rc";
const CANDIDATE = new RegExp(`-${RC}\\.[0-9]+$`);

// isCandidate reports whether a tag or version is a release candidate's.
export function isCandidate(tagOrVersion: string): boolean {
  return CANDIDATE.test(tagOrVersion);
}

// SHORT_SHA_LENGTH is how much of a commit a label shows: git's default
// abbreviation, as the console's build label shows it.
export const SHORT_SHA_LENGTH = 7;

// What a dev build appends after -dev: its commit time and commit,
// 39.2.1-dev.1792562188.g9b36704a. A label names the commit once, after it.
const DEV_BUILD_SUFFIX = /\.\d+\.g[0-9a-f]+$/;

// devBuildLabel is a dev build as the console labels it, v39.2.1-dev (622e006),
// or the commit alone when version is no dev build's.
export function devBuildLabel(sha: string, version?: string): string {
  const short = sha.slice(0, SHORT_SHA_LENGTH);
  if (version === undefined || !isDevVersion(version)) {
    return short;
  }
  return `v${version.replace(DEV_BUILD_SUFFIX, "")} (${short})`;
}
