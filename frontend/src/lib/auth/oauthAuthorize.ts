import consoleContract from "../../../server/contract/contract.json";

/**
 * Where the consent page sends the user's decision. The console's server
 * answers this path itself and relays it to the IAM plane
 * (server/internal/web), so the form works on the console's own origin
 * whichever host the IAM plane is on.
 */
export const OAUTH_AUTHORIZE_PATH = consoleContract.authorizePath;
