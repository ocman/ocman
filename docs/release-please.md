# Release Please

This repository uses `release-please` to turn Conventional Commit messages on `main` into a release pull request, a semver tag, and a Forgejo release entry.

## Files

- `.github/workflows/release-please.yml`: installs Node and runs the `release-please` CLI on pushes to `main`
- `release-please-config.json`: tells `release-please` to manage this repository as a single `simple` component
- `.release-please-manifest.json`: stores the last released version
- `.github/workflows/release.yml`: builds archives when a `v*` tag is pushed and uploads them to the Forgejo release

## One-Time Setup

1. Create a Forgejo access token for a user or bot that can open pull requests, create tags, and create releases in this repository.
2. Add that token to repository secrets as `RELEASE_PLEASE_TOKEN`.
3. Make sure the token has enough repository write access for contents, pull requests, and releases.
4. Push the workflow and config files to `main`.
5. Trigger the `Release Please` workflow once with `workflow_dispatch` if you want to verify the setup immediately.

## Why The Workflow Uses The CLI

Forgejo runners often mirror `uses:` actions through `data.forgejo.org`.

`googleapis/release-please-action` is not available there, so the workflow runs `release-please` through `npx` instead of using the third-party action directly.

The workflow performs the same two logical steps as the action:

1. `manifest-release`: create tags and release entries after a release PR has been merged.
2. `manifest-pr`: open or update the next release PR.

## Commit Format

Use Conventional Commits on changes that should affect versioning.

- `fix: correct tmux session lookup` -> patch release
- `feat: add project detail page` -> minor release
- `feat!: change session API shape` -> major release

Breaking changes can also be written with a footer:

```text
feat: change session payload

BREAKING CHANGE: session responses now omit archived messages by default
```

Commits that do not match Conventional Commits may still be included in the changelog, but they do not reliably drive version bumps.

## Normal Release Flow

1. Merge or push Conventional Commit changes onto `main`.
2. The `Release Please` workflow runs.
3. If there are releasable commits, it opens or updates a release PR.
4. Review that PR. It contains the proposed version bump and `CHANGELOG.md` updates.
5. Merge the release PR.
6. `release-please` creates the new `v*` tag and the matching Forgejo release entry.
7. The existing `Release` workflow sees the new tag, builds the platform archives, and uploads them as release assets.

## Direct Pushes To Main

If you push directly to `main`, the flow is still PR-based.

1. Your direct push triggers the `Release Please` workflow.
2. `release-please` opens or updates the release PR for the accumulated commits.
3. Nothing is tagged yet.
4. You still need to merge the generated release PR.
5. After that merge, the tag and release are created, which then triggers the packaging workflow.

Direct pushes do not skip the release PR. They only feed new commits into it.

## Why A Dedicated Token Is Required

The workflow uses `RELEASE_PLEASE_TOKEN` instead of the default automation token so that the tag created by `release-please` can trigger the separate tag-based release pipeline.

If you use the default automation token and your Forgejo instance suppresses follow-up workflow triggers from automation-created refs, the tag may be created without starting `.github/workflows/release.yml`.

## First Release

`.release-please-manifest.json` starts at `0.0.0` because the repository currently has no tags.

On the first merged release PR, `release-please` will calculate the first real version from the Conventional Commits currently on `main`.

## Troubleshooting

If no release PR appears:

1. Confirm the workflow ran on `main`.
2. Confirm `RELEASE_PLEASE_TOKEN` exists and has write access.
3. Confirm at least one merged commit follows Conventional Commit format such as `fix:` or `feat:`.
4. Re-run the `Release Please` workflow manually.

If the release PR merges but no packaged assets appear:

1. Confirm a `v*` tag was created.
2. Confirm `.github/workflows/release.yml` ran for that tag.
3. Check whether the token used to create the tag is allowed to trigger follow-up workflows on your Forgejo instance.
