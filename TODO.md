# TODO

## Git Change Index Coverage

- Index all open PRs in `openclaw/openclaw` into the git-change layer so search results are globally useful for the active review surface.
- Backfill a meaningful slice of recent closed PRs in `openclaw/openclaw` so `state=all` searches have stronger recall.
- Add an operator workflow for bulk PR indexing and re-indexing so coverage does not depend on one-off manual `sync pr` runs.
- Refactor the gradual-fill worker into an explicit per-repo coordinator with separate fetch and backfill state instead of one loosely shared repo pass.
- Add guaranteed lease cleanup plus lease heartbeat updates so a repo cannot stay stuck in a synthetic `fetch_in_progress` state after an interrupted pass.
- Persist the open-PR cursor after every successful batch and preserve it across webhook churn so coverage can continue steadily toward zero missing PRs.
- Persist the fetched open-PR set in a durable repo-local inventory so backfill batches stop re-listing every open PR from GitHub.
- Remove the full open-PR rescan before and after every backfill batch and make the worker walk the stored inventory instead.
- Split candidate discovery, canonical metadata refresh, and git-change rebuild so one PR sync does not always pay for every expensive step.
- Shrink the per-PR work unit so one slow PR cannot monopolize a whole repo batch beyond its timeout budget.

## Indexing Status Visibility

- Expose clear repo-level indexing status so callers can tell whether search results are complete, partial, or stale.
- Expose PR-level indexing status and freshness so a missing match can be distinguished from an unindexed or stale PR.
- Include index coverage/freshness in the API surface used for search so clients can interpret results without guessing.
- Make `open_pr_total`, `open_pr_current`, `open_pr_stale`, and `open_pr_missing` update during a running batch instead of only at batch boundaries.
- Tie repo-level status counters to the durable open-PR inventory so row counts and reported progress stay consistent while backfill is active.

## Search Quality

- Add path weighting and noise suppression for low-signal files such as lockfiles, generated files, vendored paths, and broad config churn.
- Improve related-PR ranking so strong code overlap outranks incidental shared files.
- Keep validating path-overlap and range-overlap results against real `openclaw/openclaw` PRs as coverage expands.
- Expose repo-level text-search indexing coverage so operators can tell whether an older mirrored repo has already been rebuilt into `search_documents`.
- Improve `mentions` ranking so strong title hits and short direct matches outrank weak body matches in long documents.
- Improve fuzzy search quality so approximate matches are stronger without requiring `pg_trgm` on managed Postgres.
- Improve excerpt generation and highlighting so long discussion threads show a more useful local snippet.
- Add explicit operator workflows for repository-wide text-index rebuilds and freshness checks.

## Observability

- Expose the current inventory generation, cursor position, and currently processing PR in repo-level status or operator diagnostics.
- Track per-PR rebuild duration, timeout counts, and last successful cursor advance so long-running backfills are understandable without DB spelunking.
- Distinguish whether the worker is discovering candidates, refreshing canonical metadata, or rebuilding git snapshots when reporting in-progress work.
