// A quota cap as the form holds it, and as the API spells it: a cap that is
// set is a limit, where 0 allows nothing; an absent one is no cap.

/** One cap in the form: the typed limit, and whether it is lifted. */
export interface CapField {
  value: string;
  unlimited: boolean;
}

/** The form field for a stored cap; no cap at all reads as Unlimited. */
export function capFieldOf(cap: bigint | undefined): CapField {
  return cap === undefined
    ? { value: "", unlimited: true }
    : { value: cap.toString(), unlimited: false };
}

const WHOLE_NUMBER = /^\d+$/;

/**
 * The cap a field sends: undefined for Unlimited, the limit otherwise, and
 * null for a limit that is not a whole number — which the form holds rather
 * than sending a number nobody typed.
 */
export function capOf(field: CapField): bigint | undefined | null {
  if (field.unlimited) return undefined;
  const typed = field.value.trim();
  return WHOLE_NUMBER.test(typed) ? BigInt(typed) : null;
}

/** used as a percentage of cap, 0–100; a cap of 0 is full from the start. */
export function capPercent(used: bigint, cap: bigint): number {
  const FULL = 100;
  if (cap <= 0n) return FULL;
  return Math.min(FULL, (Number(used) / Number(cap)) * FULL);
}
