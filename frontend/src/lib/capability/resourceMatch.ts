/**
 * The dot-segment rule limes holds every capability resource to (resource.go,
 * hasDotSegment): Caveats.Validate refuses a resource with a "." or ".."
 * segment, and MatchResource reaches no resource with one, however the dot is
 * spelled. sdk/testdata/capability_resource_matches.json, which limes and the
 * Python fake are held to, holds this helper to the same rows, so the console
 * refuses such a grant before the server does.
 */

/** A "." percent-encoded, in lower case. */
const DOT_ENCODED = "%2e";

/** Starts a segment's path parameters, and that percent-encoded. */
const PATH_PARAMETERS = ";";
const PATH_PARAMETERS_ENCODED = "%3b";

/** Every spelling a consumer reads as "/": encoded, back slash, encoded back. */
const SEGMENT_SEPARATORS = ["%2f", "\\", "%5c"] as const;
const SEPARATOR = "/";

/** The segments RFC 3986 resolves away. */
const DOT_SEGMENTS: ReadonlySet<string> = new Set([".", ".."]);

/** How many percent-decodings a consumer may apply before it resolves. */
const CONSUMER_DECODES = 2;

const PERCENT = "%".charCodeAt(0);
const HEX_DIGITS = 2;
const HEX = /^[0-9a-fA-F]{2}$/;

const encoder = new TextEncoder();
const decoder = new TextDecoder();

/**
 * s with every well-formed %XX decoded and any other "%" kept, byte for byte
 * as limes decodes it: a %XX ending s is decoded only with a byte after it.
 */
function percentDecode(s: string): string {
  const raw = encoder.encode(s);
  const out: number[] = [];
  for (let i = 0; i < raw.length; i++) {
    if (raw[i] === PERCENT && i + HEX_DIGITS < raw.length) {
      const hex = String.fromCharCode(raw[i + 1]!, raw[i + 2]!);
      if (HEX.test(hex)) {
        out.push(parseInt(hex, 16));
        i += HEX_DIGITS;
        continue;
      }
    }
    out.push(raw[i]!);
  }
  return decoder.decode(new Uint8Array(out));
}

/**
 * Whether s has a segment limes reads as "." or "..": %2e as a dot, every
 * separator spelling as "/", a segment up to its path parameters, in s and
 * after each of up to two percent-decodings.
 */
export function hasDotSegment(s: string): boolean {
  for (let pass = 0; ; pass++) {
    let lowered = s.toLowerCase();
    for (const spelled of SEGMENT_SEPARATORS) {
      lowered = lowered.replaceAll(spelled, SEPARATOR);
    }
    for (const segment of lowered.split(SEPARATOR)) {
      const bare = segment
        .replaceAll(PATH_PARAMETERS_ENCODED, PATH_PARAMETERS)
        .split(PATH_PARAMETERS, 1)[0]!
        .replaceAll(DOT_ENCODED, ".");
      if (DOT_SEGMENTS.has(bare)) return true;
    }
    if (pass === CONSUMER_DECODES) return false;
    s = percentDecode(s);
  }
}
