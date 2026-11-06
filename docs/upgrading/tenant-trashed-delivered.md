## Unreleased — `paladin.tenant.trashed` reaches subscribers when it happens

- A tenant in the trash is frozen, and its pending events wait for a restore.
  The event announcing the trash waited with them, so a subscriber learnt a
  tenant had been trashed only after it was restored, and never if it was
  purged. `paladin.tenant.trashed` is now delivered at once; every other
  event of a trashed tenant still waits.
