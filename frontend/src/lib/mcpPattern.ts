// MCP tool patterns as the server reads them (backend/internal/mcp
// PatternMatch): "*" matches every tool, "prefix*" every tool whose name
// starts with prefix, anything else exactly one name. The wildcard counts
// only at the end, so "a*b" is a literal name.

const WILDCARD = "*";

/** Whether the entry names a family of tools rather than one tool. */
export function isPattern(pattern: string): boolean {
  return pattern.endsWith(WILDCARD);
}

/** The prefix a pattern matches on; a literal name is returned whole. */
export function patternPrefix(pattern: string): string {
  return isPattern(pattern) ? pattern.slice(0, -WILDCARD.length) : pattern;
}

/** Whether the pattern selects the named tool. */
export function patternMatches(pattern: string, name: string): boolean {
  return isPattern(pattern)
    ? name.startsWith(patternPrefix(pattern))
    : pattern === name;
}

/** The first entry of a list that selects the named tool, if any. */
export function matchingEntry(
  patterns: readonly string[],
  name: string,
): string | undefined {
  return patterns.find((p) => patternMatches(p, name));
}
