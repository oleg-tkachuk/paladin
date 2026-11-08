## v42.1.0, sdk/python/v0.143.0, sdk/go/v0.143.0 — a refund emits paladin.capability.refunded

- With `dispatcher.charge_events_enabled` on, a refund that returns spend now
  emits `paladin.capability.refunded`, on the refund's own transaction. The
  server refunds every call that fails after charging, so a subscriber summing
  `paladin.capability.charged` amounts must subtract these, or it counts spend
  the ledger has returned. A filter matching `kind == "capability"` receives
  both.
- `paladin.capability.charged` gains `data.charge_id`, the charge's ledger id,
  which a refund event names as the charge it returns from.
