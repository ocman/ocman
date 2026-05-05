# Release Changelog Generation

## Background

The release pipeline (`.github/workflows/ci.yml` `tag` job +
`.github/workflows/release.yml`) already derives a semver bump from the
conventional-commit subjects in `<latest-tag>..HEAD` and pushes a
`vX.Y.Z` tag, which in turn triggers the release build. The release
that ends up on Forgejo currently has its body hard-coded to
`"Release ${TAG_NAME}"`, so the user-facing artefact does not reflect
the work that actually went into the release.

The same commit range that drives the bump decision contains enough
information (`feat:` / `fix:` / `perf:` / `refactor:` / `BREAKING
CHANGE`) to render a categorised changelog. We want to surface that
to consumers of the release.

## Goal

Generate a categorised changelog from the conventional commits in the
release range and use it as:

1. The body of the annotated git tag created by the `tag` job.
2. The body of the published Forgejo release in the `release` job.

No `CHANGELOG.md` file in the repo (release-body only).

## Functional requirements

- **R1.** When the `tag` job decides to cut `vX.Y.Z`, it MUST render a
  changelog covering `<previous-tag>..HEAD` (or the full history if
  this is the first tag).
- **R2.** The git tag pushed by the `tag` job MUST be annotated, with
  the rendered changelog as its message (so `git show vX.Y.Z` displays
  it).
- **R3.** The `release` job MUST use the same rendered changelog as
  the body of the Forgejo release, replacing the current
  `"Release ${TAG_NAME}"` placeholder.
- **R4.** The changelog MUST group commits into sections in this
  order, hiding empty sections:
  1. **Breaking changes** — anything matched by `BREAKING CHANGE:` or
     `<type>(scope)!:`
  2. **Features** — `feat:`
  3. **Bug fixes** — `fix:`
  4. **Performance** — `perf:`
  5. **Maintenance** — `refactor:`
- **R5.** The following commit types MUST be excluded from the
  rendered changelog: `chore`, `docs`, `test`, `ci`, `build`, `style`,
  `lint`. Commits that match no conventional-commit prefix MUST also
  be excluded.
- **R6.** Scopes (e.g. `feat(server): ...`) MUST be rendered as
  `**server**: ...` inside the section so readers can spot subsystem
  changes.
- **R7.** Section headings MUST be plain text (no emoji), to match
  repo conventions.
- **R8.** The release body MUST end with a `**Full changelog**:` link
  comparing the previous tag to the new tag on the configured Forgejo
  instance. This line is appended by the workflow (not by
  `cliff.toml`) so the changelog template stays host-agnostic.
- **R9.** When `<previous-tag>..HEAD` contains no commits matching the
  visible types in R4, the `tag` job MUST behave as today (refuse to
  cut a tag) — i.e. the changelog generation MUST NOT cause an empty
  release.

## Non-functional requirements

- **N1.** `git-cliff` is the rendering tool. It is installed in CI via
  `orhun/git-cliff-action@v3` and locally via `mise` (added to
  `mise.toml`).
- **N2.** The changelog template lives in a single `cliff.toml` at the
  repo root. No inline templates in workflow YAML.
- **N3.** Local maintainers MUST be able to preview the next
  release's changelog with `git cliff --unreleased` after running
  `mise install`.
- **N4.** The `tag` job's existing bump logic MUST remain authoritative
  for *whether* a tag is cut and *what* version it is. `git-cliff`'s
  own bump inference is NOT used; it only renders.

## Out of scope

- Maintaining a `CHANGELOG.md` file in the repo.
- Changing the bump decision rules (still: `BREAKING CHANGE` or `!:`
  → major, `feat` → minor, `fix|perf|refactor` → patch).
- Migrating the release pipeline to GoReleaser.
- Cross-platform release notes formatting beyond Forgejo Markdown.
