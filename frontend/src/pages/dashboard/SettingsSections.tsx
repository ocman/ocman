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
import { api, type ModelFallthroughSettings } from '../../lib/api';
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
          setting="system-notifications"
          desc={notifBlocked
            ? 'Notifications are blocked by your browser. Allow them in your browser\u2019s site settings to enable this option.'
            : undefined}
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
      <SettingRow setting="bell-sound">
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
      <SettingRow setting="start-screen-time-range">
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
      <SettingRow setting="recent-sessions-window">
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
      <SettingRow setting="message-metadata">
        <SettingToggle
          ariaLabel="Show metadata between message sections"
          checked={showMessageMetadata}
          save={messageMetadataSave}
          onSave={(next) => setShowMessageMetadata(next)}
        />
      </SettingRow>
      <SpeechSettings />
      <SettingRow setting="worktree-inherit-permissions">
        <SettingToggle
          testId="worktree-inherit-toggle"
          ariaLabel="Worktree sessions inherit parent permissions"
          checked={inheritPerms}
          save={inheritPermsSave}
          onSave={(next) => handleInheritToggle(next)}
        />
      </SettingRow>
      <SettingRow setting="auto-archive">
        <SettingToggle
          ariaLabel="Automatically archive inactive sessions and projects"
          checked={autoArchive.enabled}
          disabled={!autoArchiveLoaded || autoArchiveSaving}
          save={autoArchiveToggleSave}
          onSave={(enabled) => saveAutoArchive({ ...autoArchive, enabled })}
        />
      </SettingRow>
      {autoArchiveLoaded && autoArchive.enabled && (
        <SettingRow setting="auto-archive-after">
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
      <ModelFallthroughSettings />
    </>
  );
}

function ModelFallthroughSettings() {
  const [times, setTimes] = useState<ModelFallthroughSettings | null>(null);
  const patienceSave = useSettingSave();
  const fallbackSave = useSettingSave();
  useEffect(() => {
    const ctrl = new AbortController();
    api.getModelFallthroughSettings(ctrl.signal).then(setTimes).catch(() => { /* best-effort; rows stay hidden */ });
    return () => ctrl.abort();
  }, []);
  if (!times) return null;
  const save = async (next: ModelFallthroughSettings) => {
    const previous = times;
    setTimes(next);
    try {
      await api.setModelFallthroughSettings(next);
    } catch (err) {
      setTimes(previous);
      throw err;
    }
  };
  return (
    <>
      <SettingRow setting="model-fallthrough-patience">
        <SettingNumber
          ariaLabel="Model fallthrough patience in minutes"
          unit="min"
          min={1}
          max={1440}
          value={times.patienceMinutes}
          save={patienceSave}
          onSave={(patienceMinutes) => save({ ...times, patienceMinutes })}
        />
      </SettingRow>
      <SettingRow setting="model-fallthrough-cooldown">
        <SettingNumber
          ariaLabel="Model fallthrough cooldown in minutes"
          unit="min"
          min={1}
          max={1440}
          value={times.fallbackMinutes}
          save={fallbackSave}
          onSave={(fallbackMinutes) => save({ ...times, fallbackMinutes })}
        />
      </SettingRow>
    </>
  );
}
