# Releases

This repository creates release tags automatically from Conventional Commit messages on `main`.

## Files

- `.github/workflows/semantic-tag.yml`: inspects commits on `main`, computes the next semver version, and creates a `v*` tag through the Forgejo API
- `.github/workflows/release.yml`: runs when a `v*` tag is pushed, builds platform archives, creates the Forgejo release entry, and uploads the assets

## One-Time Setup

1. Create a Forgejo access token for a user or bot that can create tags in this repository.
2. Add that token to repository secrets as `RELEASE_TOKEN`.
3. Make sure the token can trigger follow-up workflows when it creates a tag.
4. Push the workflow files to `main`.
5. Trigger the `Semantic Tag` workflow with `workflow_dispatch` if you want to verify the setup immediately.

## Commit Format

Use Conventional Commits for changes that should drive versioning.

- `fix: correct tmux session lookup` -> patch release
- `feat: add project detail page` -> minor release
- `feat!: change session API shape` -> major release

Breaking changes can also be written with a footer:

```text
feat: change session payload

BREAKING CHANGE: session responses now omit archived messages by default
```

The semantic tag workflow currently treats these commit types as releasable:

- `feat` -> minor
- `fix`, `perf`, `refactor` -> patch
- breaking change marker or `!` -> major

If no releasable Conventional Commits are present since the latest `v*` tag, no new tag is created.

## Normal Release Flow

1. Merge or push Conventional Commit changes onto `main`.
2. The `Semantic Tag` workflow runs.
3. It finds the latest `v*` tag and evaluates commits since that tag.
4. It calculates the next semantic version.
5. It creates the new `v*` tag through the Forgejo API.
6. The existing `Release` workflow sees the new tag, builds the platform archives, creates the release entry, and uploads the assets.

## Direct Pushes To Main

Direct pushes to `main` are the normal case for this setup.

1. You push commits directly to `main`.
2. The `Semantic Tag` workflow runs on that push.
3. If the new commits include releasable Conventional Commits, it creates the next tag immediately.
4. That tag triggers the packaging and release workflow.

There is no release PR in this flow.

## First Release

If the repository has no `v*` tags yet, the semantic tag workflow starts from `0.0.0`.

Examples:

- first releasable `fix:` commit -> `v0.0.1`
- first releasable `feat:` commit -> `v0.1.0`
- first breaking change -> `v1.0.0`

## Why A Dedicated Token Is Required

The semantic tag workflow uses `RELEASE_TOKEN` instead of the default automation token so the created tag can trigger the separate tag-based release workflow.

If your Forgejo instance suppresses follow-up workflow triggers from automation-created refs, using the default automation token can create the tag without starting `.github/workflows/release.yml`.

## Troubleshooting

If no tag is created:

1. Confirm the workflow ran on `main`.
2. Confirm `RELEASE_TOKEN` exists and has repository write access.
3. Confirm at least one commit since the latest `v*` tag follows the releasable Conventional Commit patterns.
4. Check the workflow log for `No releasable conventional commits found`.

If the tag is created but no release assets appear:

1. Confirm the new `v*` tag exists in Forgejo.
2. Confirm `.github/workflows/release.yml` ran for that tag.
3. Confirm the token used to create the tag is allowed to trigger follow-up workflows on your Forgejo instance.
