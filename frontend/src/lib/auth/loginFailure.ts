import { Code, ConnectError } from "@connectrpc/connect";

export const INVALID_CREDENTIALS_MESSAGE = "Invalid username or password.";
export const SIGN_IN_UNAVAILABLE_MESSAGE = "Sign-in is unavailable";

// What the form shows, and the status that goes with it. The Connect code
// prefix ("[unauthenticated] …") is for logs, not for the person signing in,
// and a plane that cannot be reached is not a wrong password.
export function loginFailure(err: unknown): { status: number; error: string } {
  if (err instanceof ConnectError) {
    if (err.code === Code.Unauthenticated) {
      return { status: 401, error: INVALID_CREDENTIALS_MESSAGE };
    }
    if (err.code === Code.PermissionDenied) {
      return { status: 403, error: err.rawMessage };
    }
    return {
      status: 502,
      error: `${SIGN_IN_UNAVAILABLE_MESSAGE}: ${err.rawMessage}`,
    };
  }
  const reason = err instanceof Error ? err.message : String(err);
  return { status: 502, error: `${SIGN_IN_UNAVAILABLE_MESSAGE}: ${reason}` };
}
