/**
 * Section bodies for the Settings tab, extracted from SettingsTab so
 * that component stays within the size budget. Each section reads its
 * own store slices rather than taking them as props, keeping SettingsTab
 * a thin nav + layout shell.
 */
import { useState, useEffect } from 'react';
import { SettingRow, SettingToggle, SettingNumber } from '../../components/SettingRow';
import { useSettingSave } from '../../lib/useSaveStatus';
import { useUiStore } from '../../lib/uiStore';
import { api } from '../../lib/api';
import { SpeechSettings } from '../../components/SpeechSettings';
import {
  notificationsSupported,
  requestNotificationPermission,
} from '../../lib/useNotificationNotify';

// ---------------------------------------------------------------------------
// Notifications
// ---------------------------------------------------------------------------

export function NotificationsSection() {
  const notificationsEnabled = useUiStore((s) => s.notificationsEnabled);
  const setNotificationsEnabled = useUiStore((s) => s.setNotificationsEnabled);
  const bellEnabled = useUiStore((s) => s.bellEnabled);
  const setBellEnabled = useUiStore((s) => s.setBellEnabled);
  const notifSave = useSettingSave();
  const bellSave = useSettingSave();

  // System notification state. Tracked locally so we can re-render when
  // permission changes (the browser API has no event for that, so we
  // read it on mount and update after a request).
  const [notifPermission, setNotifPermission] = useState<NotificationPermission | 'unsupported'>(
    () => (notificationsSupported() ? Notification.permission : 'unsupported'),
  );
  const notifSupported = notifPermission !== 'unsupported';
  const notifBlocked = notifPermission === 'denied';

  async function handleNotificationsToggle(want: boolean) {
    if (!want) {
      setNotificationsEnabled(false);
      return;
    }
    // Turning on: ensure permission is granted first. If the user
    // previously denied it, the browser won't re-prompt — surface that
    // explicitly so the toggle doesn't silently fail.
    if (notifPermission === 'granted') {
      setNotificationsEnabled(true);
      return;
    }
    const result = await requestNotificationPermission();
    setNotifPermission(result);
    setNotificationsEnabled(result === 'granted');
  }

  return (
    <>
      {notifSupported && (
        <SettingRow
          label="System notifications"
          desc={notifBlocked
            ? 'Notifications are blocked by your browser. Allow them in your browser\u2019s site settings to enable this option.'
            : 'Show a desktop notification when a session finishes or needs your input. Works best after installing ocman as an app.'}
        >
          <SettingToggle
            ariaLabel="System notifications"
            checked={notificationsEnabled && notifPermission === 'granted'}
            disabled={notifBlocked}
            save={notifSave}
            onSave={(next) => handleNotificationsToggle(next)}
          />
        </SettingRow>
      )}
      <SettingRow
        label="Bell sound"
        desc="Play a bell sound when the app is not in focus and a session finishes or asks a question."
      >
        <SettingToggle
          ariaLabel="Bell sound"
          checked={bellEnabled}
          save={bellSave}
          onSave={(next) => setBellEnabled(next)}
        />
      </SettingRow>
    </>
  );
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

export function SessionsSection() {
  const dashboardTimeRangeDefault = useUiStore((s) => s.dashboardTimeRangeDefault);
  const setDashboardTimeRangeDefault = useUiStore((s) => s.setDashboardTimeRangeDefault);
  const sidebarRecentHours = useUiStore((s) => s.sidebarRecentHours);
  const setSidebarRecentHours = useUiStore((s) => s.setSidebarRecentHours);
  const showMessageMetadata = useUiStore((s) => s.showMessageMetadata);
  const setShowMessageMetadata = useUiStore((s) => s.setShowMessageMetadata);
  const timeRangeSave = useSettingSave();
  const recentSave = useSettingSave();
  const messageMetadataSave = useSettingSave();

  // Worktree inherit-permissions is a server-side setting (#101), so it
  // is loaded/saved directly via the API rather than through uiStore.
  const [inheritPerms, setInheritPerms] = useState(true);
  const inheritPermsSave = useSettingSave();
  const [autoArchive, setAutoArchive] = useState({ enabled: true, ttlDays: 7 });
  const [autoArchiveLoaded, setAutoArchiveLoaded] = useState(false);
  const autoArchiveToggleSave = useSettingSave();
  const autoArchiveTTLSave = useSettingSave();
  useEffect(() => {
    const ctrl = new AbortController();
    api
      .getWorktreeInheritPermissions(ctrl.signal)
      .then(({ enabled }) => setInheritPerms(enabled))
      .catch(() => { /* best-effort; keep default on */ });
    api
      .getAutoArchiveSettings(ctrl.signal)
      .then(setAutoArchive)
      .catch(() => { /* best-effort; keep defaults */ })
      .finally(() => {
        if (!ctrl.signal.aborted) setAutoArchiveLoaded(true);
      });
    return () => ctrl.abort();
  }, []);

  const handleInheritToggle = async (want: boolean) => {
    setInheritPerms(want); // optimistic
    try {
      await api.setWorktreeInheritPermissions(want);
    } catch (err) {
      setInheritPerms(!want); // revert
      throw err; // let SettingToggle surface the failure indicator
    }
  };

  const autoArchiveSaving = autoArchiveToggleSave.state === 'saving' || autoArchiveTTLSave.state === 'saving';

  const saveAutoArchive = async (next: { enabled: boolean; ttlDays: number }) => {
    const previous = autoArchive;
    setAutoArchive(next);
    try {
      await api.setAutoArchiveSettings(next);
    } catch (err) {
      setAutoArchive(previous);
      throw err;
    }
  };

  return (
    <>
      <SettingRow
        label="Start screen time range"
        desc="Default lookback window for the Sessions list on the start screen. The time-range buttons still override it for the current view."
      >
        <SettingNumber
          ariaLabel="Start screen time range in days"
          unit="days"
          min={1}
          max={365}
          value={Math.round((dashboardTimeRangeDefault / 24) * 10) / 10}
          parse={(raw) => raw * 24}
          save={timeRangeSave}
          onSave={(next) => setDashboardTimeRangeDefault(next)}
        />
      </SettingRow>
      <SettingRow
        label="Recent sessions window"
        desc={<>How far back the &ldquo;Recent sessions&rdquo; sidebar looks while you&apos;re inside a session.</>}
      >
        <SettingNumber
          ariaLabel="Recent sessions window in days"
          unit="days"
          min={1}
          max={365}
          value={Math.round((sidebarRecentHours / 24) * 10) / 10}
          parse={(raw) => raw * 24}
          save={recentSave}
          onSave={(next) => setSidebarRecentHours(next)}
        />
      </SettingRow>
      <SettingRow
        label="Message section metadata"
        desc="Show timestamp, duration, speed, and model after each assistant message section. The summary between turns stays visible."
      >
        <SettingToggle
          ariaLabel="Show metadata between message sections"
          checked={showMessageMetadata}
          save={messageMetadataSave}
          onSave={(next) => setShowMessageMetadata(next)}
        />
      </SettingRow>
      <SpeechSettings />
      <SettingRow
        label="Worktree sessions inherit parent permissions"
        desc="When you split a session into a worktree, seed the new session with the permissions you already approved with &ldquo;Allow always&rdquo; in the parent, so it doesn't re-prompt for them."
      >
        <SettingToggle
          testId="worktree-inherit-toggle"
          ariaLabel="Worktree sessions inherit parent permissions"
          checked={inheritPerms}
          save={inheritPermsSave}
          onSave={(next) => handleInheritToggle(next)}
        />
      </SettingRow>
      <SettingRow
        label="Automatically archive inactive sessions and projects"
        desc="Hide inactive sessions and projects after the configured number of days. Archived items remain available and can be restored."
      >
        <SettingToggle
          ariaLabel="Automatically archive inactive sessions and projects"
          checked={autoArchive.enabled}
          disabled={!autoArchiveLoaded || autoArchiveSaving}
          save={autoArchiveToggleSave}
          onSave={(enabled) => saveAutoArchive({ ...autoArchive, enabled })}
        />
      </SettingRow>
      {autoArchiveLoaded && autoArchive.enabled && (
        <SettingRow label="Archive after">
          <SettingNumber
            ariaLabel="Archive inactive sessions and projects after days"
            unit="days"
            min={1}
            max={3650}
            value={autoArchive.ttlDays}
            disabled={autoArchiveSaving}
            save={autoArchiveTTLSave}
            onSave={(ttlDays) => saveAutoArchive({ ...autoArchive, ttlDays })}
          />
        </SettingRow>
      )}
    </>
  );
}
