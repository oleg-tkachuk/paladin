## Unreleased — the data and IAM planes record what an action changed

- **An audit entry of the data or IAM plane carries `state_before_json` and
  `state_after_json`**, as the admin plane's does: the object, its lock or
  its tags around an object action, the user or the user's settings around
  an IAM one, read through their own Get and redacted. A batch records its
  request alone: its operation records what each object came to.
- Each audited action that records no state says why in
  `backend/internal/app/audit_state_test.go`.
