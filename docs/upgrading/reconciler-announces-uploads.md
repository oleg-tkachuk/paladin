## Unreleased — the reconciler announces the writes it finds

- An upload the reconciler promotes after its completion never came, and a
  later write through its URL that no storage event reported, now reach
  event subscribers as `paladin.object.uploaded`, with `source` set to
  `reconciler`. They used to reach none. A promote another producer made
  first, or a settle that finds the bytes the object records, is not
  announced again.
