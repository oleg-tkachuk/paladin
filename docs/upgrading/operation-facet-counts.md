## Unreleased — operation listings narrow and count by state and type

- **`ListOperationsRequest` gains `facets` and `count_facets`** on both the
  admin and the data plane. `facets` (`OperationFacets`) narrows the listing
  to the states and types it names; with `count_facets` the first page's
  answer carries `facet_counts` (`FacetCount`): for facet `state` each
  `OperationState`'s name, for `type` each type, each facet counted without
  its own choices. Additive; a caller that sets neither sees no change.
- The console's operations page filters by state and type with counts in
  place of its single-choice state filter.
