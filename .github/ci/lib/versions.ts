// What marks a dev build's version: the check that refuses to publish one as a
// release, and the cleanup that deletes only dev builds, both read it here.
// The task library derives <next patch>-dev.<…> after a release, and
// <x.y.z>-rc.<n>.dev.<…> after a release candidate, so that the build sorts
// after the candidate and before its release.
export const DEV_MARKS = ["-dev.", ".dev."] as const;

// isDevVersion reports whether version is a dev build's.
export function isDevVersion(version: string): boolean {
  return DEV_MARKS.some((mark) => version.includes(mark));
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

// What a dev build appends after its dev mark: its commit time and commit,
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
