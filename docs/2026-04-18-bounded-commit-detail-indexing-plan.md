---
title: Bounded Commit Detail Indexing Plan
date: 2026-04-18
status: proposed
---

# 2026-04-18 Bounded Commit Detail Indexing Plan

This document describes the intended production fix for pull request refreshes that currently time out while indexing deep commit detail.

This is a separate problem from three-lane change sync scheduling.

The scheduling refactor decides which work runs next.

This plan decides what it means for one pull request refresh to succeed, and how commit detail should degrade when it is too expensive to index fully.

## Problem

Today one pull request refresh does all of the following under one bounded deadline:

- refresh pull request metadata
- build the top-level pull request diff snapshot
- index commit metadata
- index commit-parent file detail
- index commit-parent hunk detail

That is too much work for one success condition.

The top-level pull request diff path already has explicit oversize guards.

The commit-level path does not.

That means a pull request can look small at the pull request level, but still be expensive to index because one commit, especially a merge commit, has a very large parent diff.

When that happens, the current implementation times out the whole pull request refresh and retries it later as if the failure were transient.

## Goal

The production goal should be:

- a pull request becomes `current` after the bounded pull request-level snapshot is indexed
- deep commit detail does not block pull request freshness
- oversized commit detail becomes explicit partial data, not a repeated refresh failure
- downstream tools can tell when commit detail is full, reduced, or skipped

## Core Rule

Pull request freshness must depend on a small, bounded critical path.

Commit-parent detail must be optional and degradable.

In practice that means:

- required work makes the pull request current
- optional work adds extra commit detail when it fits within fixed budgets
- optional work must never keep a pull request stale forever

## Required Versus Optional Work

The refresh contract should be split into two layers.

### Required

Required work is the minimum needed for the pull request to be considered current:

- pull request metadata
- top-level pull request diff snapshot
- top-level changed file rows
- top-level hunk rows where the pull request-level budgets allow them
- commit metadata such as commit SHA, parents, authorship, and message

If required work succeeds, the pull request refresh succeeds.

### Optional

Optional work is extra commit detail that improves analysis depth but is not required for pull request freshness:

- commit-parent file rows
- commit-parent patch text
- commit-parent hunk rows

If optional work is reduced or skipped because it is too expensive, the pull request refresh should still succeed.

## Commit Detail Levels

Commit detail should have three explicit levels:

- `full`
- `paths_only`
- `skipped`

### `full`

Store:

- commit metadata
- commit-parent file rows
- commit-parent hunk rows

Use this when the commit-parent diff fits within the fixed budgets.

### `paths_only`

Store:

- commit metadata
- commit-parent file rows

Do not store:

- commit-parent hunk rows
- large commit-parent patch text

Use this when the commit-parent diff is useful at file granularity but too expensive for hunk-level indexing.

### `skipped`

Store:

- commit metadata only

Do not store:

- commit-parent file rows
- commit-parent hunk rows

Use this when even file-level commit-parent detail would exceed the allowed budget or would force timeout-prone work.

## Commit Detail Reasons

The degraded state should be explicit.

At minimum, commit detail should carry:

- `indexed_as`
- `index_reason`

Recommended reasons:

- `within_budget`
- `budget_exceeded`
- `oversized_merge_commit`
- `timeout_avoided`

This lets downstream tooling distinguish:

- no available detail
- intentionally reduced detail
- transient failure

## Cheap Enough

Commit-parent detail should be governed by fixed hard budgets in code.

The first production version should keep those budgets internal, not operator-facing.

The budgets should be evaluated per commit-parent diff using simple inputs such as:

- changed file count
- total changed lines
- patch byte size
- whether the commit is a merge commit

The exact first-cut defaults can be adjusted in code review, but the shape should be:

- ordinary commits may get `full` detail within a moderate budget
- merge commits should use much stricter budgets
- once a budget is exceeded, degrade to `paths_only`
- if that still requires too much work, degrade to `skipped`

The important production rule is stability, not operator tuning.

These are product-shape limits, not a large new set of environment variables.

## Retry Policy

The current retry behavior is too aggressive for deterministic budget failures.

The new rules should be:

- retry transient Git, database, or network failures
- do not keep retrying `budget_exceeded`, `oversized_merge_commit`, or `timeout_avoided` as if they were transient
- treat reduced commit detail as a successful refresh outcome

That means one pathological merge commit should stop burning repeated five-minute refresh attempts.

## Success Rule

A pull request refresh should succeed when:

- required pull request-level work completes
- commit metadata is stored
- optional commit detail is either fully stored or explicitly degraded

A pull request refresh should fail only when required work fails.

That keeps pull request freshness tied to the data most downstream tools actually need.

## Output Contract

The reduced detail must be obvious in the output.

The exact surface can be finalized during implementation, but the intended shape is:

- pull request-level status indicates whether commit detail is complete
- commit-level records indicate `indexed_as`
- commit-level records indicate `index_reason`

For example, the changes/status surface should make it clear that:

- the pull request is current
- some commit detail was reduced or skipped intentionally

This should not pollute the GitHub-compatible read API for GitHub-native resources.

If extra metadata is needed, it should live on explicit `changes` surfaces or extension fields.

## Implementation Direction

The implementation should follow this order:

1. split required pull request-level indexing from optional commit-detail indexing
2. add commit-detail states and reasons to the data model
3. add commit-parent budgeting and merge-commit degradation rules
4. stop treating deterministic over-budget commit detail as refresh failure
5. update status output and tests to make partial commit detail explicit

This should be a direct cutover, not a dual-path design.

## Testing

Coverage should include:

- small ordinary pull requests that still get full commit detail
- oversized ordinary commits that degrade to `paths_only`
- oversized merge commits that degrade to `paths_only` or `skipped`
- pull request refreshes that still succeed when commit detail is reduced
- no repeated retry loop for deterministic budget outcomes
- honest status output for partial commit detail

Production verification should specifically confirm:

- targeted refresh no longer repeatedly times out on pathological merge-commit pull requests
- pull request `current` coverage resumes climbing
- logs stop showing the same deterministic timeout every retry window

## Non-Goal

This plan does not change the GitHub-compatible surface for pull request objects.

It changes the internal change-index contract and the explicit change-status surface.

The goal is to keep the GitHub-shaped read surface clean while making indexing behavior bounded, honest, and reliable under production load.
