## Unreleased — cancelling an operation that is not there is NOT_FOUND

- `CancelOperation` on an operation the tenant does not have now answers
  `NOT_FOUND`, as `GetOperation` does. It used to answer
  `FAILED_PRECONDITION`, which is still the answer for an operation that has
  already finished. A client that treated `FAILED_PRECONDITION` as "gone"
  should handle `NOT_FOUND` too.
