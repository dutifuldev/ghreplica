# TODO

## Git Change Index Coverage

- Index all open PRs in `openclaw/openclaw` into the git-change layer so search results are globally useful for the active review surface.
- Backfill a meaningful slice of recent closed PRs in `openclaw/openclaw` so `state=all` searches have stronger recall.
- Add an operator workflow for bulk PR indexing and re-indexing so coverage does not depend on one-off manual `sync pr` runs.
- Refactor the gradual-fill worker into an explicit per-repo coordinator with separate fetch and backfill state instead of one loosely shared repo pass.
- Add guaranteed lease cleanup plus lease heartbeat updates so a repo cannot stay stuck in a synthetic `fetch_in_progress` state after an interrupted pass.
- Persist the open-PR cursor after every successful batch and preserve it across webhook churn so coverage can continue steadily toward zero missing PRs.

## Indexing Status Visibility

- Expose clear repo-level indexing status so callers can tell whether search results are complete, partial, or stale.
- Expose PR-level indexing status and freshness so a missing match can be distinguished from an unindexed or stale PR.
- Include index coverage/freshness in the API surface used for search so clients can interpret results without guessing.

## Search Quality

- Add path weighting and noise suppression for low-signal files such as lockfiles, generated files, vendored paths, and broad config churn.
- Improve related-PR ranking so strong code overlap outranks incidental shared files.
- Keep validating path-overlap and range-overlap results against real `openclaw/openclaw` PRs as coverage expands.
