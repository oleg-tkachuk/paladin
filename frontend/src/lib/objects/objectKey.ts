/**
 * The object key rule, as the server holds a key to it where an object is
 * created (backend/internal/storage/storagepath, ValidateKey): at most
 * MAX_KEY_BYTES bytes of UTF-8 without control characters, in segments of at
 * most MAX_SEGMENT_BYTES separated by "/", none of them empty, "." or "..".
 * sdk/testdata/object_keys.json holds the server, both SDKs' fakes and this
 * helper to the same cases, so every console form that takes a key or a key
 * prefix checks it here, before it sends anything.
 */

/** S3's 1024-byte path less the tenant id, the longest collection segment
 *  and two separators: a key this long fits in every collection. */
export const MAX_KEY_BYTES = 731;

/** The longest file name a filer-backed store (SeaweedFS) takes. */
export const MAX_SEGMENT_BYTES = 255;

export const KEY_SEPARATOR = "/";

const CURRENT_SEGMENT = ".";
const PARENT_SEGMENT = "..";

// In a "u" regular expression a well-formed surrogate pair is one code point,
// so \p{Cs} matches only a lone surrogate, which no UTF-8 encodes.
const LONE_SURROGATE = /\p{Cs}/u;
const CONTROL_CHARACTER = /\p{Cc}/u;

/** The sentence a refused key is answered with, as the server words it. */
export const KEY_RULE = `An object key is at most ${MAX_KEY_BYTES} bytes of UTF-8 without control characters, in segments of at most ${MAX_SEGMENT_BYTES} bytes separated by "/", none of them empty, "." or "..".`;

const encoder = new TextEncoder();

/** The length of s in UTF-8 bytes, which the store counts, not characters. */
export function utf8Bytes(s: string): number {
  return encoder.encode(s).length;
}

/** Why s is not key text of at most maxBytes, or null. */
function textProblem(s: string, maxBytes: number): string | null {
  if (LONE_SURROGATE.test(s)) return "it is not valid UTF-8";
  const size = utf8Bytes(s);
  if (size > maxBytes)
    return `it is ${size} bytes, over the limit of ${maxBytes}`;
  if (CONTROL_CHARACTER.test(s)) return "it holds a control character";
  return null;
}

/** Why one of segments may not be a key segment of at most maxBytes, or null. */
function segmentsProblem(segments: string[], maxBytes: number): string | null {
  for (const s of segments) {
    if (s === "") return "it holds an empty segment";
    if (s === CURRENT_SEGMENT) return 'it holds a "." segment';
    if (s === PARENT_SEGMENT) return 'it holds a ".." segment';
    const size = utf8Bytes(s);
    if (size > maxBytes) {
      return `it holds a segment of ${size} bytes, over the limit of ${maxBytes}`;
    }
  }
  return null;
}

/**
 * Why the server would refuse key where an object is created, or null when it
 * stores an object under it.
 */
export function objectKeyProblem(key: string): string | null {
  if (key === "") return "it is empty";
  const text = textProblem(key, MAX_KEY_BYTES);
  if (text) return text;
  if (key.startsWith(KEY_SEPARATOR)) return 'it starts with "/"';
  if (key.endsWith(KEY_SEPARATOR)) return 'it ends with "/"';
  return segmentsProblem(key.split(KEY_SEPARATOR), MAX_SEGMENT_BYTES);
}

/**
 * Why the server would refuse prefix as the start of every key of a batch, or
 * null. Only its complete segments are held to the key rule; the key
 * completes the last one, so the prefix leaves room for at least one byte, in
 * that segment and in all. The empty prefix is no prefix.
 */
export function keyPrefixProblem(prefix: string): string | null {
  if (prefix === "") return null;
  const text = textProblem(prefix, MAX_KEY_BYTES - 1);
  if (text) return text;
  if (prefix.startsWith(KEY_SEPARATOR)) return 'it starts with "/"';
  const segments = prefix.split(KEY_SEPARATOR);
  const last = segments.pop() ?? "";
  const complete = segmentsProblem(segments, MAX_SEGMENT_BYTES);
  if (complete) return complete;
  const size = utf8Bytes(last);
  if (size > MAX_SEGMENT_BYTES - 1) {
    return `it holds a segment of ${size} bytes, over the limit of ${MAX_SEGMENT_BYTES - 1}`;
  }
  return null;
}
