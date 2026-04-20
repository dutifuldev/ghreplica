---
title: Recent PR Repair Mode
date: 2026-04-20
status: proposed
---

# Recent PR Repair Mode

## Problem

For large active repositories such as `openclaw/openclaw`, `ghreplica` currently favors webhook-driven updates plus bounded open-PR repair work.

That keeps the system cheap, but it has one clear failure mode:

- a PR can be mirrored while open
- the later `closed` or `merged` webhook can be missed or not applied
- the PR can stay incorrectly `open` in `ghreplica`

This is a real correctness problem for downstream tools that rely on PR state.

## Goal

Keep the current webhook-first model, but add a bounded repair path that can heal missed PR state transitions for selected repositories.

The fix should:

- stay per-repo
- stay bounded
- include recently updated closed PRs
- avoid turning every tracked repository back into a full crawler

## Proposed Shape

Extend the existing per-repo backfill mode rather than inventing a second mode switch.

Suggested modes:

- `open_only`
  - existing webhook-first plus open-PR inventory and repair
- `open_and_recent`
  - existing `open_only` behavior plus the daily recent-PR repair pass
- `full_history`
  - existing `open_and_recent` behavior plus a page-by-page full-history repair pass

Default:

- `off`

Example opt-ins:

- enable `open_and_recent` for a repo that needs stronger close-state correctness
- enable `full_history` for `openclaw/openclaw` when we want that repo to keep walking all PRs over time

## How It Works

When `backfill_mode` is `open_and_recent` or `full_history`, the worker should run a daily repair pass over a bounded window of recently updated pull requests and pull-request-backed issues for that repository, even if those pull requests are no longer open.

The repair pass should work in two stages:

1. keep the existing webhook projection flow
2. keep the existing open-PR inventory and backfill flow
3. list recent pull requests from GitHub using `state=all`, sorted by `updated desc`
4. list recent issues from GitHub using `state=all`, sorted by `updated desc`, limited to issue rows that correspond to pull requests
5. compare GitHub `updated_at` with the stored `pull_requests.github_updated_at` and `issues.github_updated_at`
6. fetch the full PR resource only when the PR row is missing or stale
7. fetch the full issue resource only when the issue row is missing or stale
8. update only the rows whose GitHub object actually changed
9. advance a repair cursor so the work stays bounded and incremental

Recommended starting policy:

- run once per day
- cover PRs updated in the last 7 days
- use the list endpoints only as a cheap stale detector
- fetch full objects only for stale PR rows or stale issue rows
- keep the window configurable per repo if needed later

This repair path should also be manually triggerable for one repository, even if that repository is not opted into the automatic daily mode.

That gives operators two paths:

- automatic daily repair for opted-in repos
- an immediate repair run when stale PR state needs to be corrected now

This repair pass should repair the GitHub PR object and the matching GitHub issue object.

That means it should cover fields that can change on either side, including:

- PR state
- merged state
- merged timestamp
- close timestamp
- title and body
- labels, assignees, and similar issue-backed metadata
- head and base metadata that GitHub returns on the PR object
- the stored `updated_at` values on both rows

This should stay a metadata repair path, not a full rebuild of every PR-adjacent resource.

It should not refresh comments, reviews, review comments, search documents, or other heavier derived data as part of the daily recent repair pass.

## Why This Is The Right Shape

This is the clean middle ground.

It avoids the two bad extremes:

- depending entirely on webhook delivery for close-state correctness
- re-enabling full-history PR crawling for every tracked repository

It also avoids a third bad shape:

- fully re-projecting every recent PR even when GitHub `updated_at` shows nothing changed

It gives stronger correctness only to repositories that need it, without adding constant background pressure.

## Non-Goals

This mode should not:

- become the default for all repositories
- trigger whole-repo scans on every webhook
- replace the current open-PR inventory logic
- promise full historical completeness for all PR-adjacent resources
- fully re-sync every recent PR object on every pass

## Repository Policy

Recommended policy:

- most repositories stay on the cheaper default
- high-activity repos that matter for downstream triage can opt into recent PR repair

Initial target:

- `openclaw/openclaw`

## Verification

A successful rollout should show:

- known stale PRs flipping from `open` to `closed`
- known stale PR-backed issue rows flipping from `open` to `closed`
- no broad increase in repo-wide sync cost
- no regression in existing webhook or open-PR repair behavior

Good verification examples:

- compare several known stale PRs against GitHub before the change
- compare both the PR row and the matching issue row against GitHub before the change
- enable `open_and_recent` or `full_history` for one repo
- wait for at least one repair cycle
- confirm those PRs and issue rows now match GitHub state in `ghreplica`

## Operational Rule

This mode is for correctness repair, not bulk completeness.

If a repository needs broad historical coverage, that should still be handled by an explicit backfill strategy.

Recent PR repair exists to correct the kind of stale state that webhook-first mirroring can otherwise leave behind.

## Manual Trigger

The repair pass should be callable on demand for one repository.

That trigger should:

- use the same recent-PR repair logic as the scheduled path
- stay bounded to the same recent window unless an operator explicitly changes the settings
- be safe to run even if the daily schedule also exists

This is important for cases where operators already know a repository has stale PR state and do not want to wait for the next daily run.
