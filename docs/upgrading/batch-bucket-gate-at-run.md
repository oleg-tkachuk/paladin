## Unreleased — batches ask the bucket gate again when they run

- The tag, restore and trash batches fail when their collection's backend was
  disabled after the batch was submitted, and write nothing. A permanent
  delete batch also fails on a draining backend or a bucket not ready for
  writes, where it used to fail each object on its own.
- A batch the gate refuses ends `FAILED` with the refusal's reason as its
  error code (`ERROR_REASON_BACKEND_DISABLED`, `ERROR_REASON_BACKEND_READ_ONLY`,
  `ERROR_REASON_BUCKET_PROVISIONING` or `ERROR_REASON_FAILED_PRECONDITION`),
  and the operation's error answers the refusal's own code
  (`FAILED_PRECONDITION` or `UNAVAILABLE`) rather than `UNKNOWN`. A batch copy
  refused at run time is answered the same way.
- The worker logs a refused batch at Warn and any other failed batch at Error
  (it was Warn).
