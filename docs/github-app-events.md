# GitHub App Events

This document records the full GitHub App event list visible from the GitHub App creation UI during `ghreplica` setup on `2026-04-14`.

It is a reference list of possible GitHub-side webhook subscriptions, not the recommended minimal set for `ghreplica`.

## Current Minimal ghreplica Set

For the current `ghreplica` deployment, the intended initial event subset is:

- `Issue comment`
- `Issues`
- `Pull request`
- `Pull request review`
- `Pull request review comment`
- `Push`
- `Repository`

The rest of this document is the full event inventory from the GitHub App UI.

## Full Event List

- `Installation target`
  - A GitHub App installation target is renamed.
- `Meta`
  - When this App is deleted and the associated hook is removed.
- `Security advisory`
  - Security advisory published, updated, or withdrawn.
- `Check run`
  - Check run is created, requested, rerequested, or completed.
- `Check suite`
  - Check suite is requested, rerequested, or completed.
- `Commit comment`
  - Commit or diff commented on.
- `Create`
  - Branch or tag created.
- `Delete`
  - Branch or tag deleted.
- `Deploy key`
  - A deploy key is created or deleted from a repository.
- `Deployment`
  - Repository was deployed or a deployment was deleted.
- `Deployment protection rule`
  - Deployment protection rule requested for an environment.
- `Deployment review`
  - Deployment review requested, approved or rejected.
- `Deployment status`
  - Deployment status updated from the API.
- `Fork`
  - Repository forked.
- `Gollum`
  - Wiki page updated.
- `Issue comment`
  - Issue comment created, edited, or deleted.
- `Issues`
  - Issue opened, edited, deleted, transferred, pinned, unpinned, closed, reopened, assigned, unassigned, labeled, unlabeled, milestoned, demilestoned, locked, unlocked, typed, untyped, field_added, or field_removed.
- `Label`
  - Label created, edited or deleted.
- `Milestone`
  - Milestone created, closed, opened, edited, or deleted.
- `Merge group`
  - Merge Group requested checks, or was destroyed.
- `Merge queue entry`
  - Merge Queue entry added.
- `Public`
  - Repository changes from private to public.
- `Pull request`
  - Pull request assigned, auto merge disabled, auto merge enabled, closed, converted to draft, demilestoned, dequeued, edited, enqueued, labeled, locked, milestoned, opened, ready for review, reopened, review request removed, review requested, synchronized, unassigned, unlabeled, or unlocked.
- `Pull request review`
  - Pull request review submitted, edited, or dismissed.
- `Pull request review comment`
  - Pull request diff comment created, edited, or deleted.
- `Pull request review thread`
  - A pull request review thread was resolved or unresolved.
- `Push`
  - Git push to a repository.
- `Registry package`
  - Registry package published or updated in a repository.
- `Release`
  - Release created, edited, published, unpublished, or deleted.
- `Repository`
  - Repository created, deleted, archived, unarchived, publicized, privatized, edited, renamed, or transferred.
- `Repository dispatch`
  - When a message is dispatched from a repository.
- `Star`
  - A star is created or deleted from a repository.
- `Watch`
  - User stars a repository.
- `Workflow dispatch`
  - A manual workflow run is requested.
- `Workflow job`
  - Workflow job queued, waiting, in progress, or completed on a repository.
- `Workflow run`
  - Workflow run requested or completed on a repository.
- `Sub issues`
  - Sub-issues added or removed, and parent issues added or removed.
