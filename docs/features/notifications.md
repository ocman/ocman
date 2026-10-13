---
title: Notifications
weight: 18
---

Ocman can send OS notifications when a session finishes or needs a permission
decision or an answer to a question. Clicking an alert opens the session on its
owning machine, including prompts from a session's children.
On an existing window the conversation is revealed, including when a mobile
details panel was open. The link's owner takes precedence over cached sessions.

## App icon count

On supported installed PWAs, the icon shows the number of distinct unread or
attention sessions. A session needing both permission and question input counts
once; same-id sessions on different owners count separately. Reading or resolving
the sessions clears the badge when the app refreshes its notification state.
Push alerts also carry the current count so a closed app can update its badge.
Without a new push, changes made on another device refresh when this app opens.
While open, the app dismisses session alerts once their unread/input state is
resolved. Notifications newer than the fetched snapshot remain until a fresh
snapshot confirms their state, so cleanup cannot erase a newly arrived alert.

Numeric badges depend on the browser and operating system. iOS/iPadOS and
supported desktop browsers can show a number. Android generally shows the OS's
notification dot instead; the browser does not provide exact numeric control.

## Enable on each device

Open **Settings → Notifications** and turn on **System notifications**. Accept
the browser's permission prompt. On browsers with Web Push support, this also
registers the device for alerts while the ocman page is closed. If notifications
were already enabled before background push was available, click **Enable
background alerts** in the same row.

On iPhone and iPad, use iOS/iPadOS 16.4 or later, add ocman to your Home Screen,
then open the installed app to enable notifications. Use HTTPS for devices
accessing ocman over the network. Localhost is also a secure context on desktop.

Enable notifications separately on every device you want to receive alerts.
Turning the setting off removes that device's server subscription and
unsubscribes its browser. Other devices keep their subscriptions.
If browser cleanup fails after server removal, page notifications keep working
and Settings offers registration again. The page confirms server registration
before suppressing its own alerts, including after a reload.

## Delivery

Ocman's backend must remain running and have outbound HTTPS access to the
browser's push service. Firebase accounts, native apps and an inbound public
ocman endpoint are not required. An HTTPS Tailscale address works; the device
still needs access to that address when opening a notification.

The backend checks the same notification snapshot as the dashboard every ten
seconds, only while devices are subscribed, with push-specific eligibility for
completed shell envelopes. Incomplete or stale owner reads preserve the previous
baseline. A separate joined worker delivers alerts, so slow push services do not
delay observation. The first snapshot after startup
or after subscribing the first device is a baseline, so existing completed
sessions and pending prompts do not cause a flood of historical alerts. New
alerts use the session title, a status such as "Finished" or "Permission needed",
the project/worktree name, and a session link. The OS already identifies ocman as
the sender, so the notification does not repeat its name. Transcript text,
commands and permission details are not included.

Remote permission alerts use the owning machine's judge decision. Upgrade the
remote ocman too to receive these alerts; owners without that decision metadata
are kept quiet rather than asking for input on a permission still being judged.

VAPID signing keys, subscriptions, pending deliveries and deduplication receipts
live in this installation's `state.db`. Pending sends survive a restart;
transient failures retry up to six attempts with backoff. Undelivered alerts
expire after an hour, push-service retention is five minutes, and expired
subscriptions are removed automatically. OS notification permissions, Focus
modes and browser restrictions can delay or prevent delivery.

Subscribed devices receive session alerts through push instead of also showing
the page's polling notifications. Browsers without Web Push retain notifications
while the page is running. Factory action notifications remain page-driven.

While ocman is visible and focused, page-driven system alerts are suppressed;
in-app alerts remain available. Web Push still displays a notification because
browsers require one for every push, but requests no sound or vibration and
does not keep input-request alerts sticky while you are using ocman. Browser
and OS support for silent notifications varies. When ocman is hidden or
unfocused, notifications use their normal behaviour.
