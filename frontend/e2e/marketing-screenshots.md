# Marketing screenshots

Capture the current frontend with fictional projects, sessions and messages:

```sh
cd frontend
pnpm install --frozen-lockfile
pnpm build
UPDATE_MARKETING_SCREENSHOTS=1 pnpm exec playwright test e2e/marketing-screenshots.spec.ts --project chromium --workers 1
```

This starts only a checkout-specific Vite preview server. Use the production
bundle, including for the lazy-loaded diff renderer. No Go backend, agent runtime,
database or real account is needed. API requests use the shared e2e stubs and
marketing fixtures. A context-level guard aborts any API request that escapes
the stubs and any external HTTP request. Service workers are blocked, and the
event stream is a quiet browser-local fake.

The capture fixes the viewport, clock, locale and timezone and disables
animations. It checks that the intended content is visible and that known
personal paths and old sample data are absent. Inspect both images before
publishing, especially after changing the fixtures or UI.

Outputs:

- `docs/sessions.png`, the homepage conversation and social preview.
- `site/static/screenshots/session-browser.png`, the homepage session browser.
- `site/static/screenshots/changes.png`, a fictional patch, file list and linked PR.
- `site/static/screenshots/analytics.png`, synthetic usage and cost metrics.

Without `UPDATE_MARKETING_SCREENSHOTS=1`, tests write images only to ignored
Playwright test results. Normal CI never rewrites the published assets.
