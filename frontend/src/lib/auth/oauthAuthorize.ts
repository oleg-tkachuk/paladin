/**
 * Where the consent page sends the user's decision. The console serves this
 * path itself and relays it to the IAM plane (src/app/oauth/authorize), so the
 * form works on the console's own origin whichever host the IAM plane is on.
 */
export const OAUTH_AUTHORIZE_PATH = "/oauth/authorize";
