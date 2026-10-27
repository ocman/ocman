---
title: Releases
weight: 9
---

Release tags are created automatically from Conventional Commits on `main`,
after the frontend, backend and end-to-end CI jobs pass.

## Files

- `.forgejo/workflows/ci.yml`: the **Semantic Tag** job uses git-cliff to calculate the next version, render the changelog, and push an annotated `v*` tag at the tested commit.
- `cliff.toml`: commit grouping, filtering, changelog formatting and version-bump policy.
- `.forgejo/workflows/release.yml`: a pushed `v*` tag builds platform archives and the macOS desktop DMG, creates the Forgejo release, and uploads assets. Release notes come from the annotated tag message; an empty message falls back to a minimal release note.

## One-time setup

1. Create a Forgejo personal access token that can push tags and trigger follow-up workflows for this repository.
2. Save it as the repository secret **`RELEASE_PAT`**.
3. Push a releasable Conventional Commit to `main` and inspect the **CI → Semantic Tag** job after the test jobs pass.

The workflow uses `RELEASE_PAT` for checkout/push credentials and tag lookups,
falling back to the default automation token when it is absent. The shell
variable `RELEASE_TOKEN` is populated from that selection; it is not the name
of a repository secret. If the default token cannot trigger another workflow,
a tag can be pushed without starting release packaging.

There is no separate Semantic Tag workflow or `workflow_dispatch` trigger.
The tag steps run only on pushes to `main`, not on pull-request events.

## Commit format

Use Conventional Commits for changes that should drive versioning:

| Commit | Version change |
| --- | --- |
| `fix: correct tmux session lookup` | Patch |
| `perf: reduce session query work` | Patch |
| `refactor: simplify runtime lookup` | Patch |
| `feat: add project detail page` | Minor |
| `feat!: change session API shape` | Major |

A `BREAKING CHANGE:` footer also marks a breaking change. The configured bump
policy keeps feature bumps minor and breaking bumps major even before 1.0.
Routine `docs`, `test`, `ci`, `build`, `style`, `lint` and `chore` commits are
hidden from release notes; breaking commits are protected by the git-cliff
configuration. Non-conventional messages are filtered out.

## Normal release flow

1. Integrate changes onto `main` with linear history: squash, rebase or fast-forward; no merge commits.
2. CI runs the frontend, backend and Playwright jobs.
3. The **Semantic Tag** job compares the git-cliff version with the latest tag and skips an unchanged or already-existing version.
4. It renders the unreleased changelog and pushes an annotated tag at the tested commit.
5. The **Release** workflow packages that tag and publishes its assets and notes.

Direct pushes to `main` use the same checks and tag job. There is no release PR.

## Troubleshooting

If no tag is created:

1. Confirm CI ran for a push to `main` and all three prerequisite jobs passed.
2. Inspect **Semantic Tag** for a skipped unchanged/existing version or a tag lookup/push error.
3. Check that commits since the latest `v*` tag match the patterns in `cliff.toml`.
4. Confirm `RELEASE_PAT` has repository write access if the default token is insufficient.

If the tag exists but assets do not:

1. Confirm the tag triggered `.forgejo/workflows/release.yml`.
2. Check whether the selected token is allowed to trigger follow-up workflows.
3. Inspect the frontend, platform binary, desktop packaging and publish jobs for failures.
