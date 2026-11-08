## Unreleased — events are matched to subscriptions by the dispatcher

- A producer now records each event once, in a new `event_outbox` table
  (migration 107), and the dispatcher matches it to subscriptions. The
  change that emits an event no longer fails when a subscription is deleted
  at the same moment, and a storage backend's credential rotation no longer
  fails when the dispatcher is wired.
- A subscription receives an event if it existed when the event was
  recorded; one created a moment later does not. See
  `docs/event-delivery-dedup.md`.
- A stalled fan-out does not raise `paladin_outbox_pending`; the new
  `paladin_outbox_recorded` gauge counts the events waiting for it, and
  `PaladinEventFanOutNotDraining` fires on it.
