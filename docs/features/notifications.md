---
title: Notifications
weight: 18
---

Ocman can send OS notifications when a session finishes or needs a permission
decision or an answer to a question. Clicking an alert opens the session on its
owning machine, including prompts from a session's children.

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

## Delivery

Ocman's backend must remain running and have outbound HTTPS access to the
browser's push service. Firebase accounts, native apps and an inbound public
ocman endpoint are not required. An HTTPS Tailscale address works; the device
still needs access to that address when opening a notification.

The backend checks the same notification snapshot as the dashboard every ten
seconds, only while devices are subscribed. The first snapshot after startup
or after subscribing the first device is a baseline, so existing completed
sessions and pending prompts do not cause a flood of historical alerts. New
alerts contain fixed text and a session link, never titles, transcript text,
commands or permission details.

VAPID signing keys, subscriptions, pending deliveries and deduplication receipts
live in this installation's `state.db`. Pending sends survive a restart;
transient failures retry up to six attempts with backoff. Undelivered alerts
expire after an hour, push-service retention is five minutes, and expired
subscriptions are removed automatically. OS notification permissions, Focus
modes and browser restrictions can delay or prevent delivery.

Subscribed devices receive session alerts through push instead of also showing
the page's polling notifications. Browsers without Web Push retain notifications
while the page is running. Factory action notifications remain page-driven.
