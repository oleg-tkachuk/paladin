"use client";

import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { useQueryClient } from "@tanstack/react-query";

import { FormDialog } from "@/components/ui/form-dialog";
import { NoticeFields } from "@/components/features/notice/NoticeFields";
import { useNotification } from "@/components/ui/Notification";
import { useBumpRefresh } from "@/context/RefreshContext";
import { BUCKETS_QUERY_KEY } from "@/lib/queries/buckets";
import { BucketSchema, type Bucket } from "@/gen/paladin/admin/v1/types_pb";
import { errorMessage } from "@/hooks/errorContract";
import { bucketClient } from "@/lib/connect/client";
import { fieldMask } from "@/lib/connect/fieldMask";
import {
  labelsWithNoticeForm,
  noticeFormFor,
  noticeLinkError,
  sameLabels,
  type NoticeForm,
} from "@/lib/noticeForm";

/**
 * The bucket's notice, as its page and every bucket list show it
 * (notice.paladin.io/* labels; lib/notice), edited as a tenant's is. Every
 * other label is sent back as it was, under the bucket's version, so a
 * change made since it was read is refused rather than overwritten. Mounted
 * when it opens, so its fields start from the bucket as it is then.
 */
export function BucketNoticeDialog({
  bucket,
  open,
  onClose,
  onSaved,
}: {
  bucket: Bucket;
  open: boolean;
  onClose: () => void;
  onSaved: (bucket: Bucket) => void;
}) {
  const { showNotification } = useNotification();
  const qc = useQueryClient();
  const bumpRefresh = useBumpRefresh();
  const [form, setForm] = useState<NoticeForm>(() =>
    noticeFormFor(bucket.labels),
  );
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const labels = labelsWithNoticeForm(bucket.labels, form);
  const unchanged = sameLabels(labels, bucket.labels);
  const badUrl = noticeLinkError(form);

  const save = async () => {
    setSubmitError(null);
    setSubmitting(true);
    try {
      const saved = await bucketClient.updateBucket({
        name: bucket.name,
        resourceVersion: bucket.resourceVersion,
        updateMask: fieldMask(BucketSchema, "labels"),
        bucket: create(BucketSchema, { labels }),
      });
      onSaved(saved);
      // Every bucket list shows the notice beside the bucket.
      void qc.invalidateQueries({ queryKey: BUCKETS_QUERY_KEY });
      bumpRefresh("buckets");
      showNotification({
        type: "success",
        title: "Notice saved",
        message: bucket.bucketId,
      });
      onClose();
    } catch (err) {
      setSubmitError(errorMessage(err, "Failed to save the notice."));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
      title="Edit notice"
      description="The operator's word on this bucket, shown on its page and in every bucket list."
      onSubmit={() => void save()}
      submitLabel="Save notice"
      submittingLabel="Saving…"
      submitting={submitting}
      blockedReason={
        unchanged ? "No change to save." : badUrl ? "Fix the link." : null
      }
      error={submitError}
    >
      <NoticeFields
        form={form}
        onChange={setForm}
        where="Shown on the bucket's page and beside it in every bucket list."
      />
    </FormDialog>
  );
}
