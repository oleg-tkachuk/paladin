import { getOption } from "@bufbuild/protobuf";

import { field as validateField } from "@/gen/buf/validate/validate_pb";
import { CreateBucketRequestSchema } from "@/gen/paladin/admin/v1/bucket_service_pb";

// The rules CreateBucketRequest.bucket_id declares, read from the generated
// descriptor so the form and the API cannot disagree.
const rules = (() => {
  const r = getOption(
    CreateBucketRequestSchema.field.bucketId,
    validateField,
  ).type;
  if (r.case !== "string") {
    throw new Error("bucket_id has no string rules in the contract");
  }
  return {
    minLen: Number(r.value.minLen),
    maxLen: Number(r.value.maxLen),
    pattern: new RegExp(r.value.pattern),
  };
})();

/** Why the API would refuse this bucket name, or null when it would not. */
export function bucketNameError(name: string): string | null {
  if (name.length < rules.minLen || name.length > rules.maxLen) {
    return `${rules.minLen}–${rules.maxLen} characters.`;
  }
  if (!rules.pattern.test(name)) {
    return "Lowercase letters, digits, dots and hyphens; start and end with a letter or digit.";
  }
  return null;
}
