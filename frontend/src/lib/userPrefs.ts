// What the profile page needs to offer and check a user's preferences, and a
// new password, before the server sees them. The server stays the authority:
// these turn a refusal into an inline message, nothing more.

import { getOption } from "@bufbuild/protobuf";

import { field as validateField } from "@/gen/buf/validate/validate_pb";
import { ChangePasswordRequestSchema } from "@/gen/paladin/iam/v1/auth_service_pb";

// Offered where the runtime cannot list its zones (Intl.supportedValuesOf).
const FALLBACK_TIMEZONES = [
  "UTC",
  "Europe/Kyiv",
  "Europe/London",
  "Europe/Berlin",
  "America/New_York",
  "America/Los_Angeles",
  "Asia/Tokyo",
  "Asia/Singapore",
  "Australia/Sydney",
];

/** Locales offered as suggestions; any BCP-47 tag can still be typed. */
export const COMMON_LOCALES = [
  "en-US",
  "en-GB",
  "uk-UA",
  "de-DE",
  "fr-FR",
  "ja-JP",
];

/** Every IANA zone this runtime knows, UTC first. */
export function timeZoneOptions(): string[] {
  const listed =
    typeof Intl.supportedValuesOf === "function"
      ? Intl.supportedValuesOf("timeZone")
      : FALLBACK_TIMEZONES;
  return ["UTC", ...listed.filter((z) => z !== "UTC")];
}

/**
 * The rules ChangePasswordRequest.new_password declares, read from the
 * generated descriptor so the form and the API cannot disagree.
 */
export const NEW_PASSWORD_MIN_LENGTH = (() => {
  const r = getOption(
    ChangePasswordRequestSchema.field.newPassword,
    validateField,
  ).type;
  if (r.case !== "string") {
    throw new Error("new_password has no string rules in the contract");
  }
  return Number(r.value.minLen);
})();
