# Console voice

How the console's messages read: notifications, confirmations, empty
states and errors. `frontend/src/lib/voice.test.ts` holds every literal
notification to the rules marked *checked*.

## Say what happened, to what

- A title names the change and its subject: "Tenant deleted", "Upload
  failed", "Subscription changed elsewhere". Not "Success", "Done!" or
  "Action performed". *(checked: no "success")*
- Sentence case. Only the first word and proper names are capitalised:
  "Retention not applied", not "Retention Not Applied". *(checked)*
- A title has no closing period; a message is one or two full sentences.
  *(checked)*
- No exclamation marks, no "please", no apology. *(checked)*

## A failure says why and what next

- The message is the reason, as the server gave it, or what the reader can
  do: "The latest version is loaded; make the change once more."
- Name the step that failed ("Trash failed", "Purge failed"), not a generic
  "Action failed", wherever the code knows which it was.

## Confirm where it happened

- A success the reader can see on screen is confirmed beside the control
  that made it — "Saved" beside a Save button, a tick on a copy button —
  through `useConfirmation` and `Confirmation`, not a toast in a corner.
- A toast is for what has no place on screen: a change made from a menu
  that closed, a background operation, an Undo.
- A change shown before the server answers (`lib/optimistic`) needs no
  confirmation of its own: the row already reads as it will.

## Words

- "Trash" for the recoverable delete, "purge" for the permanent one
  ([deletion-semantics.md](deletion-semantics.md)).
- "Changed elsewhere" for a write refused on a stale resource version.
