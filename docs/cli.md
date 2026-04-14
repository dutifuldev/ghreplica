# CLI

This document describes the intended CLI for `ghreplica`.

The CLI should be a thin read client over the `ghreplica` HTTP API.

It should not invent a new object model, a new output format, or a separate local cache by default.

## Goal

The goal is:

- let users read mirrored repository data from terminals
- make `ghreplica` usable in scripts and shell workflows
- preserve GitHub-compatible shapes and output
- feel familiar to users who already know `gh`

For data that already exists on GitHub, the CLI should present it in the same shape and format as GitHub CLI wherever practical.

That means:

- command families should mirror `gh`
- flags should prefer `gh` naming
- JSON output should use the same field names GitHub exposes
- text output should match `gh` conventions as closely as possible

## Design Rule

The server is the product surface.

The CLI is just a convenience client.

So the CLI should:

- call `ghreplica` endpoints
- format responses the same way `gh` does
- avoid duplicating business logic from the server

The CLI should not:

- maintain its own interpretation of GitHub objects
- silently fetch from GitHub directly when data is missing from `ghreplica`
- invent `ghreplica`-specific response shapes for GitHub-native data

## Command Shape

The command tree should follow the `gh` mental model.

Recommended starting shape:

- `ghr issue list`
- `ghr issue view`
- `ghr pr list`
- `ghr pr view`
- `ghr repo view`

The first target is read-only parity for the endpoints `ghreplica` already serves.

Examples:

```bash
ghr repo view openclaw/openclaw
ghr issue list openclaw/openclaw --state all
ghr issue view openclaw/openclaw 66797
ghr pr list openclaw/openclaw --state all
ghr pr view openclaw/openclaw 66795
```

## Compatibility Expectations

For the overlapping read surface, the CLI should try to match `gh` in:

- command names
- positional argument patterns
- core flags
- JSON field names
- human-readable output ordering and labels

This is especially important for:

- `issue list`
- `issue view`
- `pr list`
- `pr view`
- `repo view`

If `gh` prints GitHub-native data in a certain shape, `ghr` should aim to print the mirrored data the same way.

## Output Modes

The CLI should support two primary output modes.

### 1. Human-readable output

This should be the default.

It should mimic `gh` as closely as possible for:

- headings
- tables
- labels
- ordering
- summary lines

### 2. Structured output

This should support:

- `--json`
- later, possibly `--jq`
- later, possibly `--template`

The JSON output should preserve GitHub field names and nested object structure.

## Configuration

The CLI should accept:

- `--base-url`
- optional auth configuration later if the hosted mirror requires it

Reasonable defaults:

- `GHR_BASE_URL`
- default local base URL for development
- explicit base URL for hosted environments such as `https://ghreplica.dutiful.dev`

## Implementation Guidance

Use `Cobra` as the CLI framework.

Why:

- it is the standard choice for multi-command Go CLIs
- it matches the command-tree style we want
- GitHub CLI itself uses a Cobra-based command layout
- it keeps help, completions, and nested subcommands straightforward

So the intended stack for the read CLI is:

- `Cobra` for command and flag structure
- a thin HTTP client for talking to `ghreplica`
- formatting code that mirrors `gh` output conventions

Avoid inventing a custom parser layer unless Cobra becomes a real limitation.

Use the local `gh` CLI source as the UX reference:

- `~/repos/gh-cli/pkg/cmd/issue`
- `~/repos/gh-cli/pkg/cmd/pr`
- `~/repos/gh-cli/pkg/cmd/repo`

The goal is not to copy implementation details blindly.

The goal is to copy the user-facing contract:

- same command family layout
- same flags where the data surface overlaps
- same output style where possible

## Initial Scope

Start with the endpoints already supported by the server:

- repository view
- issue list
- issue view
- issue comments
- pull request list
- pull request view
- pull request reviews
- pull request review comments

That is enough to make the CLI useful for debugging, triage, and agent workflows.

## Non-Goals

For the first version, do not add:

- write operations
- local sync logic
- GitHub fallback reads
- a second schema layer
- repo bootstrap controls in the read CLI

Operator functions can stay on the main `ghreplica` binary.

The read CLI should stay focused on consuming mirrored data.
