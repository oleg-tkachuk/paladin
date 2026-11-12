import { SessionError } from "@/lib/session/client";

export const INVALID_CREDENTIALS_MESSAGE = "Invalid username or password.";
export const SIGN_IN_UNAVAILABLE_MESSAGE = "Sign-in is unavailable";

// The session role's Connect codes, as its endpoints answer them.
const CODE_UNAUTHENTICATED = "unauthenticated";
const CODE_PERMISSION_DENIED = "permission_denied";

// What the sign-in form shows for a refusal. The Connect code is for logs,
// not for the person signing in, and a plane that cannot be reached is not a
// wrong password.
export function loginFailure(err: unknown): string {
  if (err instanceof SessionError) {
    if (err.code === CODE_UNAUTHENTICATED) return INVALID_CREDENTIALS_MESSAGE;
    if (err.code === CODE_PERMISSION_DENIED) return err.message;
    return `${SIGN_IN_UNAVAILABLE_MESSAGE}: ${err.message}`;
  }
  const reason = err instanceof Error ? err.message : String(err);
  return `${SIGN_IN_UNAVAILABLE_MESSAGE}: ${reason}`;
}
