/**
 * The scopes a user consents to on the OAuth consent page, and what each lets
 * the client do (ADR-0035). The names are the server's
 * (backend/internal/auth/oauth_scope.go), which enforces them on every call.
 */

export const OAUTH_SCOPE_READ = "paladin.read";
export const OAUTH_SCOPE_WRITE = "paladin.write";

const OAUTH_SCOPE_DESCRIPTIONS: Readonly<Record<string, string>> = {
  [OAUTH_SCOPE_READ]: "Read what your account can read",
  [OAUTH_SCOPE_WRITE]: "Create, change and delete what your account can change",
};

/** What a scope allows, in words; a scope the console does not know shows as itself. */
export function describeOAuthScope(scope: string): string {
  return OAUTH_SCOPE_DESCRIPTIONS[scope] ?? scope;
}

/** Whether a grant of these scopes may change anything. */
export function grantsWrite(scopes: readonly string[]): boolean {
  return scopes.includes(OAUTH_SCOPE_WRITE);
}
