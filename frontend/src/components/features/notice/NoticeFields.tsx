"use client";

import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { FormField, FormRow, FormSection } from "@/components/ui/form-dialog";
import { NOTICE_SEVERITIES, type NoticeSeverity } from "@/lib/notice";
import { hasNotice, noticeLinkError, type NoticeForm } from "@/lib/noticeForm";
import { T } from "@/lib/ui/typography";

const SEVERITY_LABEL: Record<NoticeSeverity, string> = {
  info: "Info",
  warning: "Warning",
  critical: "Critical",
};

/**
 * A notice's fields, as every resource that carries one edits it. `where`
 * says where the notice shows, in the text hint under it.
 */
export function NoticeFields({
  form,
  onChange,
  where,
}: {
  form: NoticeForm;
  onChange: (form: NoticeForm) => void;
  where: string;
}) {
  const set = <K extends keyof NoticeForm>(key: K, value: NoticeForm[K]) =>
    onChange({ ...form, [key]: value });
  const saying = hasNotice(form);
  return (
    <FormSection title="Notice">
      <FormField label="Notice" hint={`${where} Empty for none.`}>
        {(control) => (
          <Textarea
            {...control}
            rows={2}
            placeholder="Read-only while it moves to the new backend."
            value={form.notice}
            onChange={(e) => set("notice", e.target.value)}
          />
        )}
      </FormField>
      <FormRow>
        <FormField label="Severity">
          {(control) => (
            <SelectRoot
              value={form.severity}
              onValueChange={(v) => set("severity", v as NoticeSeverity)}
              disabled={!saying}
            >
              <SelectTrigger {...control}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {NOTICE_SEVERITIES.map((s) => (
                  <SelectItem key={s} value={s}>
                    {SEVERITY_LABEL[s]}
                  </SelectItem>
                ))}
              </SelectContent>
            </SelectRoot>
          )}
        </FormField>
        <FormField
          label="Link"
          error={noticeLinkError(form)}
          hint="Where Read more goes."
        >
          {(control) => (
            <Input
              {...control}
              placeholder="https://status.example.com"
              value={form.url}
              disabled={!saying}
              onChange={(e) => set("url", e.target.value)}
            />
          )}
        </FormField>
      </FormRow>
      <div className="flex items-center gap-2">
        <Switch
          aria-label="Permanent"
          checked={form.permanent}
          disabled={!saying}
          onCheckedChange={(v) => set("permanent", v)}
        />
        <span className={T.hint}>
          {form.permanent
            ? "Permanent: nobody can dismiss it"
            : "Each reader can dismiss it"}
        </span>
      </div>
    </FormSection>
  );
}
