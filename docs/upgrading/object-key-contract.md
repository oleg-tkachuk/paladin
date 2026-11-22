## Unreleased — every object has a storage path of its own

- A nested collection occupies one segment of the bucket path: its `/` is
  written `.`. Collection `invoices/2026/q1` stores key `report.pdf` at
  `<tenant>/invoices.2026.q1/report.pdf`, where it used to share
  `<tenant>/invoices/2026/q1/report.pdf` with key `2026/q1/report.pdf` of
  collection `invoices`. Collections without a `/` keep their layout.
  `Object.placement.storage_path` reports the new path.
- Objects of nested collections uploaded before this release are still
  listed, but their bytes are where the old layout put them, so reading them
  fails. Delete such objects and upload them again; on a cluster whose data
  is disposable, delete the nested collections and create them again. Their
  old bytes are not removed by Paladin.
