// The billing period in the URL, so a link — from every tenant's spend to
// one tenant's, or one shared — opens on the same period.
import { DEFAULT_PRESET, PRESETS, type PresetKey } from "./period";

/** The query parameters a period is kept in. */
export const PERIOD_PARAM = "period";
export const FROM_PARAM = "from";
export const TO_PARAM = "to";

/** A period as the page holds it: a preset, or a custom span of dates. */
export interface PeriodChoice {
  preset: PresetKey;
  /** yyyy-mm-dd, as a date input reads them; set for a custom period. */
  from: string;
  to: string;
}

const PRESET_KEYS = new Set<string>(PRESETS.map((p) => p.key));

/** The period a URL's query names; anything it does not name is the default. */
export function periodFromParams(params: URLSearchParams): PeriodChoice {
  const raw = params.get(PERIOD_PARAM) ?? "";
  const preset = PRESET_KEYS.has(raw) ? (raw as PresetKey) : DEFAULT_PRESET;
  return {
    preset,
    from: preset === "custom" ? (params.get(FROM_PARAM) ?? "") : "",
    to: preset === "custom" ? (params.get(TO_PARAM) ?? "") : "",
  };
}

/**
 * params with choice written into them, other parameters kept. The default
 * preset is left out, so the plain address stays the plain address.
 */
export function withPeriod(
  params: URLSearchParams,
  choice: PeriodChoice,
): URLSearchParams {
  const next = new URLSearchParams(params.toString());
  next.delete(PERIOD_PARAM);
  next.delete(FROM_PARAM);
  next.delete(TO_PARAM);
  if (choice.preset !== DEFAULT_PRESET) next.set(PERIOD_PARAM, choice.preset);
  if (choice.preset === "custom") {
    if (choice.from) next.set(FROM_PARAM, choice.from);
    if (choice.to) next.set(TO_PARAM, choice.to);
  }
  return next;
}

/** "?…" for params, or "" when they hold nothing. */
export function querySuffix(params: URLSearchParams): string {
  const q = params.toString();
  return q ? `?${q}` : "";
}
