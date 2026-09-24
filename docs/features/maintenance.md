---
title: Database maintenance
weight: 90
---

OpenCode's database grows fast. Whenever a turn changes files, OpenCode stores
a full patch of every changed file on that turn's user message. It then stores
the patch again in every `message.updated` event in its event log. A checkout
that moves across a large upstream change can add hundreds of megabytes in a
single turn.

**Settings → Maintenance** removes those patches from sessions that haven't
been updated for 30 days, then compacts the database. The patches are kept in
a dump, so you can put them back.

## What is lost

Only OpenCode's own web app reads the patches, for its per-turn "changes"
view. For cleaned sessions, that view, `GET /session/{id}/diff?messageID=`, and
the diffs section of `opencode export` come back empty.

These are unaffected:

- ocman
- the OpenCode terminal UI
- reverting a turn, which works from snapshots
- the per-session added, removed and changed-file counts

## Clean up

**Clean up** runs these steps and shows each one's progress:

1. **Check disk space.** Refuses unless the database's volume has three times
   the database size free. That's the worst case for the backup, the dump and
   compaction together.
2. **Stop opencode.** Stops every opencode instance ocman manages and blocks
   new launches until the job ends. In-flight turns are interrupted. If any
   other process still has the database open (an opencode you started
   yourself, a `sqlite3` shell), the job lists them and stops without changing
   anything.
3. **Back up database.** Writes a consistent copy to
   `opencode.db.ocman-backup`.
4. **Dump diffs.** Copies the patches into `opencode.db.ocman-diffs`, next to
   the database.
5. **Remove diffs.** Removes only the patches that are in the dump.
6. **Compact database.** Runs `VACUUM` to return the freed space to the disk.
7. **Delete backup.** Deletes the backup once everything succeeded. After a
   failure, the backup stays. It is one self-contained file. To restore it,
   stop every opencode, delete `opencode.db-wal` and `opencode.db-shm`, then
   copy the backup over `opencode.db`.
8. **Relaunch opencode.** Restarts the instances stopped in step 2. This step
   runs even when an earlier step failed.

Running it again later adds newly old sessions to the same dump.

## Restore and delete

**Restore** stops opencode the same way and puts every dumped patch back.
**Delete dump** removes the dump file for good.

Both buttons, like Clean up, only work from a browser on the machine running
ocman. Each machine cleans its own database from its own Settings page.
