// A notice as a form edits it — its text, severity, link and whether readers
// can dismiss it — read from and written back to a resource's
// notice.paladin.io/* labels (lib/notice). One contract for every resource
// that carries one: a tenant, a bucket.

import {
  noticeFromLabels,
  noticeUrl,
  readNotice,
  withNoticeLabels,
  type NoticeSeverity,
} from "@/lib/notice";

/** A notice's fields as the form holds them. */
export interface NoticeForm {
  notice: string;
  severity: NoticeSeverity;
  url: string;
  permanent: boolean;
}

/** The form for the notice labels carry; empty when they carry none. */
export function noticeFormFor(
  labels: Record<string, string> | undefined,
): NoticeForm {
  const notice = noticeFromLabels(labels);
  return {
    notice: notice?.content ?? "",
    severity: notice?.severity ?? "info",
    url: notice?.url ?? "",
    permanent: notice?.permanent ?? false,
  };
}

/**
 * labels as the form leaves them: the notice's labels set, or removed when
 * the form says nothing, and every other label as it was.
 */
export function labelsWithNoticeForm(
  labels: Readonly<Record<string, string>>,
  form: NoticeForm,
): Record<string, string> {
  return withNoticeLabels(
    labels,
    readNotice({
      content: form.notice,
      severity: form.severity,
      url: form.url,
      permanent: form.permanent,
    }),
  );
}

/** Whether a form has a notice to say, which its other fields wait on. */
export const hasNotice = (form: NoticeForm) => form.notice.trim() !== "";

/**
 * Why the form's link cannot be saved, or null. The server stores any text;
 * the console would drop a link it cannot show safely, so it says so before
 * saving rather than losing it quietly.
 */
export function noticeLinkError(form: NoticeForm): string | null {
  return hasNotice(form) &&
    form.url.trim() !== "" &&
    noticeUrl(form.url) === undefined
    ? "Give an http(s) address or a path in the console, such as /trash."
    : null;
}

/** Whether two label sets are the same, in any order. */
export const sameLabels = (
  a: Record<string, string>,
  b: Record<string, string>,
) =>
  Object.keys(a).length === Object.keys(b).length &&
  Object.entries(a).every(([k, v]) => b[k] === v);
