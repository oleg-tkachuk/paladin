## Unreleased — the object listing narrows and counts by state and content category

- **`ListObjectsRequest` gains `facets` and `count_facets`.** `facets`
  (`ObjectFacets`) narrows the listing to the states and the content
  categories (`ObjectContentCategory`: image, video, audio, text, PDF, JSON,
  other) it names, as conjuncts of `filter`. With `count_facets` the first
  page's answer carries `facet_counts` for facets `state` and
  `content_category`, each counted without its own choices, and
  `facet_counts_exact`, false when the count stopped at its scan cap of
  10 000 objects. No counts come back for a collection whose policies decide
  object by object. Additive.
- The SDK fakes refuse `facets` and `count_facets` with `UNIMPLEMENTED`, as
  they do `filter`.
