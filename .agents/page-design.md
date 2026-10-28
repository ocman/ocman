# Page design and UX guidelines

These are the agreed defaults for Ocman pages. Apply them when adding a page or
changing an existing one. They describe the target behavior; the
[rollout checklist](../spec/page-ux-consistency/README.md) tracks remaining work.
Component and token rules live in
[frontend.md](frontend.md).

## Page structure and actions

- Keep the compact, data-first visual language. Group content with borders and
  background contrast; do not put every section in a decorative card.
- The app header owns the page title. Place section navigation below it when
  needed, followed by the page toolbar and content.
- Collection pages put their primary action in the page toolbar, near their
  filters and content. Session workspaces may use app-header actions.
- Keep row actions at the end of the row. Make destructive actions distinct.
- Share page spacing and navigation styling instead of copying feature CSS.
  Preserve route links as links even when they look like tabs.

## Loading, failure and empty states

- Use skeleton rows for initial list/table loading. Use a spinner with a label
  for small panels without a predictable layout.
- If initial loading fails, show a dedicated unavailable state with an icon,
  heading, explanation and Retry. Never show an empty-state message alongside
  a load failure.
- During background refresh, keep the last successful content visible. Show
  refresh failures in a compact warning with Retry. Do not replace existing
  rows with skeletons or a full-page error, including a successfully empty list.
- Full-page lists use a dedicated empty state with an icon, heading and helpful
  explanation. Small embedded lists may use a compact message.
- Distinguish "nothing exists yet" from "nothing matches these filters".
  Filtered empty results offer Clear filters. First-use states explain how to
  add content and offer a creation action where the page supports one.
- Treat independent lists independently: failure to load webhook inboxes must
  not turn a successfully loaded routine list into an unavailable state.

## Navigation and list interaction

- Store search, meaningful filters, selected records and tabs in the URL.
  Reload, shared links and browser Back must restore the view. Preserve
  unrelated parameters, including explicit owner/machine scope.
- Replace history entries while typing so Back does not traverse keystrokes.
  Keep drafts, secrets and unsaved form contents out of URLs.
- Browse-and-open lists support whole-row clicking and an explicit title link
  or button. Links retain normal keyboard and new-tab behavior. Other links,
  selection controls and row actions operate independently.
- Do not open a row when the user is selecting text. Tables primarily used for
  comparison or copying data may use explicit links without whole-row clicking.
- Use tables when comparing columns matters, grouped rows for queues/progress,
  and specialized workspaces for Inbox and session conversations. Do not force
  every page into the same layout.

## Drawers, forms and confirmation

- Use one shared drawer implementation. Default to 50% viewport width on
  desktop; offer an explicit 80% variant for prompts, history and complex forms.
  Adapt to the available phone width without horizontal overflow.
- Multi-field forms use explicit Save/Cancel. Warn before discarding unsaved
  edits, including closing or navigating away from an editor.
- Individual settings may autosave. Show Saving, Saved, or a failure with Retry
  beside the affected control. Do not imply success before persistence finishes.
- Use shared app dialogs instead of browser confirmations. Identify the item,
  explain known consequences and name the action, such as "Delete routine",
  rather than "OK". Initially focus Cancel for destructive actions.
- Disable duplicate submissions and show pending state. Keep errors and retry
  actions near the operation that failed.

## Phones, accessibility and language

- Browse-and-open lists become stacked rows on phones. Keep the title, key
  metadata and main action visible; put secondary details in the detail view.
  Retain scrolling tables only where column comparison is essential.
- Preserve the primary action on small screens. Adapt panels deliberately;
  horizontal scrolling alone is not the default phone layout.
- Every action works without a mouse. Name icon-only controls, show keyboard
  focus, and communicate status with text rather than color alone.
- Use capability-neutral wording unless the action genuinely belongs to one
  platform. Show explicit machine ownership where it matters.
- Recent activity uses relative time with the exact timestamp available on
  hover and keyboard focus. History/records use a shared exact-date formatter,
  with the year when different. Scheduled runs show an explicit timezone.

## Review checklist

Check initial loading, successful empty results, filtered empty results, initial
failure, refresh/pending operations and failures after data has loaded. Verify
desktop and phone layouts, keyboard operation, URL reload/Back behavior and
explicit remote ownership. Bug fixes need regression tests observed failing
before the implementation, as described in [testing.md](testing.md).
