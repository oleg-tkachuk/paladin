## v42.0.0 — folder listings walk an index

- Migration `105` builds `idx_objects_collection_path_c` on `objects
  (collection_id, path COLLATE "C")` concurrently. Uploads keep working
  while it builds; on a large `objects` table the migrate Job runs for as
  long as the build takes (minutes per ten million objects), and the
  index adds about 14% to the size of the table with its indexes.
- Migration `106` lists a collection's folders by walking that index, one
  probe per folder: the root of a 10M-object collection no longer reads
  every key. The folders returned are unchanged.
