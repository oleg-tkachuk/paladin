// What marks a dev build's version, <x.y.z>-dev.<…>: the check that refuses
// to publish one as a release, and the cleanup that deletes only dev builds,
// both read it here.
export const DEV_MARK = "-dev.";

// isDevVersion reports whether version is a dev build's.
export function isDevVersion(version: string): boolean {
  return version.includes(DEV_MARK);
}
