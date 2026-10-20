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
