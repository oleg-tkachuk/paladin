// Ceiling on how many pages one list read follows: a backstop against paging
// forever if a token ever fails to terminate, not a limit anyone should reach.
// At the maximum page size it is ten thousand rows, far past any console list
// that stays usable.
export const MAX_LIST_PAGES = 20;
