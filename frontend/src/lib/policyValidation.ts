import type { NotificationType } from "@/components/ui/Notification";

/** PolicyDiagnostic.severity for a finding that blocks saving. */
export const SEVERITY_ERROR = "error";

/**
 * The notification for a PolicyService.Validate response that came back ok.
 * ok=true with diagnostics means the policy compiles but the schema check
 * found problems — an unknown action, or a read that fails at request time.
 * Those are warnings, not "valid".
 * Returns null when ok=false: the diagnostics under the editor say why.
 */
export function validateNotice(res: {
  ok: boolean;
  diagnostics: readonly { severity: string }[];
}): { type: NotificationType; title: string; message: string } | null {
  if (!res.ok) return null;
  const warnings = res.diagnostics.filter(
    (d) => d.severity.toLowerCase() !== SEVERITY_ERROR,
  ).length;
  if (warnings === 0) {
    return {
      type: "success",
      title: "Policy is valid",
      message: "It compiles and matches the schema.",
    };
  }
  return {
    type: "warning",
    title: "Policy compiles, with warnings",
    message: `${warnings} schema ${warnings === 1 ? "finding" : "findings"}; see the diagnostics below the editor.`,
  };
}
