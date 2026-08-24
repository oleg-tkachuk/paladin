import { createRegistry } from "@bufbuild/protobuf";
import { StructSchema } from "@bufbuild/protobuf/wkt";

/**
 * Type registry for google.protobuf.Any, shared by the browser transport and
 * the BFF bridge.
 *
 * Any carries its payload's type as a URL, and @bufbuild/protobuf v2 resolves
 * nothing implicitly — not even well-known types. A JSON codec without a
 * registry cannot decode the field, and the failure is not scoped to it: one
 * undecodable Any fails the whole response.
 *
 * Both halves of the console need it. The bridge decodes each plane response
 * and re-encodes it as JSON for the browser, so it hits this on the way out;
 * the browser hits it on the way in.
 *
 * Register what the server actually packs — connectshim's jsonToAny decodes
 * each executor payload into a Struct and packs that. A type missing here
 * still fails loudly with the same message, which is the right outcome:
 * silently dropping an Any would leave a client believing an operation
 * carried no metadata at all.
 */
export const anyRegistry = createRegistry(StructSchema);
