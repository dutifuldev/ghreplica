# AGENTS.md

## Vision

`ghreplica` exists to be a durable GitHub mirror for tooling.

The project should let downstream systems work against GitHub-shaped data without each tool needing to build and maintain its own partial sync layer. The mirror should be reliable, explicit about completeness, and practical to operate for both small repos and large active repos.

`ghreplica` should stay agnostic of any one downstream consumer.

Do not shape `ghreplica` around a particular external project or product unless there is a strong general reason that improves the mirror itself.

If some downstream tool needs product-specific behavior or metadata, keep that behavior outside `ghreplica` or behind clearly separate extension surfaces.

The intended long-term shape is:

- webhook-first ingestion
- explicit and bounded backfills
- canonical GitHub-shaped storage
- GitHub-compatible read APIs
- predictable operational behavior under load

## Core Product Direction

- Default to webhook-driven projection, not full bootstrap.
- Treat full backfills as explicit operator actions, not an automatic consequence of receiving one event.
- Prefer small, targeted repairs over whole-repo recrawls.
- Preserve GitHub object identity and relationships exactly where possible.
- Be honest about partial mirrors and sync completeness.

## API Compatibility Rule

For data that already exists on GitHub, `ghreplica` should always try to mimic the GitHub API as closely as possible and should not stray from it without a strong reason.

This applies to:

- resource shapes
- field names
- nested object structure
- URL fields
- status codes
- pagination behavior
- null vs omitted behavior
- ordering semantics where clients depend on them

If GitHub already defines the contract, prefer matching GitHub over inventing a `ghreplica`-specific variant.

## Design Constraint

Do not create custom `ghreplica` API representations for GitHub-native resources unless there is a clear and unavoidable reason.

If additional product-specific metadata is needed, prefer one of these approaches:

- store it separately from canonical GitHub-shaped resources
- expose it through separate endpoints or explicit extension fields
- keep the GitHub-compatible surface clean and predictable

## Practical Rule Of Thumb

When choosing between:

- a simpler internal implementation that drifts from GitHub, and
- a slightly harder implementation that preserves GitHub compatibility

prefer the GitHub-compatible implementation for GitHub-native data.

## Documentation Convention

Follow SimpleDoc for repository documentation.

- General, non-dated documents should use capitalized filenames with underscores, for example `TESTING.md` or `GIT_GROUND_TRUTH.md`.
- Dated documents should live under `docs/` and use ISO date prefixes with lowercase kebab-case filenames, for example `docs/2026-04-15-git-ground-truth-implementation-plan.md`.
- Dated documents should include YAML frontmatter when practical.
