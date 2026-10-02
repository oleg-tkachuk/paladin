import { create, type DescMessage } from "@bufbuild/protobuf";
import { type FieldMask, FieldMaskSchema } from "@bufbuild/protobuf/wkt";

/** A field of a generated message, by its TypeScript (camelCase) name. */
export type MaskField<D extends DescMessage> = Extract<
  keyof D["field"],
  string
>;

/**
 * An update_mask naming fields of a generated message, written with their
 * TypeScript names and sent as their proto names. A literal path string is
 * invisible to the compiler, so a renamed field kept "saving" while the
 * server skipped it; a name that is not a field of the message does not
 * compile.
 */
export function fieldMask<D extends DescMessage>(
  schema: D,
  ...fields: MaskField<D>[]
): FieldMask {
  return create(FieldMaskSchema, {
    paths: fields.map((f) => schema.field[f].name),
  });
}
