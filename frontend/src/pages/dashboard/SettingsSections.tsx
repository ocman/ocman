/**
 * Section bodies for the Settings tab, extracted from SettingsTab so
 * that component stays within the size budget. Each section reads its
 * own store slices rather than taking them as props, keeping SettingsTab
 * a thin nav + layout shell.
 */
import { useState, useEffect } from 'react';
import { SettingRow, SettingToggle, SettingNumber, SettingSelect } from '../../components/SettingRow';
import { useSettingSave } from '../../lib/useSaveStatus';
import { useUiStore } from '../../lib/uiStore';
import { api } from '../../lib/api';
import { enablePush, disablePush, pushSupported, pushSubscribed } from '../../lib/pushNotifications';
import { Button } from '../../components/Control';
import { AppBadgeSettings } from '../../components/AppBadgeSettings';
import { SnoozeSettings } from '../../components/SnoozeSettings';
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
  const [backgroundEnabled, setBackgroundEnabled] = useState<boolean | null>(null);
  const [notificationError, setNotificationError] = useState('');
  useEffect(() => { void pushSubscribed().then(setBackgroundEnabled).catch(() => setBackgroundEnabled(false)); }, []);

  async function registerPush() {
    setNotificationError('');
    try {
      await enablePush();
      setBackgroundEnabled(true);
    } catch (error) {
      setNotificationError(error instanceof Error ? error.message : 'Could not enable background notifications. Retry.');
      throw error;
    }
  }

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
      try {
        await disablePush();
        setNotificationError('');
      } catch (error) {
        setBackgroundEnabled(await pushSubscribed());
        setNotificationError(error instanceof Error ? error.message : 'Could not disable notifications. Retry.');
        throw error;
      }
      setBackgroundEnabled(false);
      setNotificationsEnabled(false);
      return;
    }
    // Turning on: ensure permission is granted first. If the user
    // previously denied it, the browser won't re-prompt — surface that
    // explicitly so the toggle doesn't silently fail.
    if (notifPermission === 'granted') {
      if (pushSupported()) await registerPush();
      setNotificationsEnabled(true);
      return;
    }
    const result = await requestNotificationPermission();
    setNotifPermission(result);
    if (result === 'granted' && pushSupported()) await registerPush();
    setNotificationsEnabled(result === 'granted');
  }

  return (
    <>
      <SettingRow
        setting="system-notifications"
        desc={notificationError || (!notifSupported
          ? 'Use HTTPS and a browser with notification support. On iPhone/iPad, add ocman to your Home Screen and open it there.'
          : notifBlocked
            ? 'Notifications are blocked by your browser. Allow them in your browser\u2019s site settings to enable this option.'
            : 'Alerts for session completion and input requests. Enable on each device to receive them while the app is closed. On iPhone/iPad, install ocman on your Home Screen first.')}
      >
        <SettingToggle
          ariaLabel="System notifications"
          checked={notificationsEnabled && notifPermission === 'granted'}
          disabled={!notifSupported || notifBlocked || notifSave.state === 'saving'}
          save={notifSave}
          onSave={(next) => handleNotificationsToggle(next)}
        />
        {notificationsEnabled && notifPermission === 'granted' && pushSupported() && backgroundEnabled === false && (
          <Button size="small" disabled={notifSave.state === 'saving'} onClick={() => { void notifSave.track(registerPush).catch(() => {}); }}>Enable background alerts</Button>
        )}
      </SettingRow>
      <SettingRow setting="bell-sound">
        <SettingToggle
          ariaLabel="Bell sound"
          checked={bellEnabled}
          save={bellSave}
          onSave={(next) => setBellEnabled(next)}
        />
      </SettingRow>
      <AppBadgeSettings />
    </>
  );
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

export function ConversationSection() {
  const compactToolCalls = useUiStore((s) => s.compactToolCalls);
  const setCompactToolCalls = useUiStore((s) => s.setCompactToolCalls);
  const save = useSettingSave();
  const showMessageMetadata = useUiStore((s) => s.showMessageMetadata);
  const setShowMessageMetadata = useUiStore((s) => s.setShowMessageMetadata);
  const messageMetadataSave = useSettingSave();
  return (
    <>
    <SettingRow setting="compact-tool-calls">
      <SettingToggle
        ariaLabel="Compact tool calls"
        checked={compactToolCalls}
        save={save}
        onSave={setCompactToolCalls}
      />
    </SettingRow>
    <SettingRow setting="message-metadata">
      <SettingToggle ariaLabel="Show metadata between message sections" checked={showMessageMetadata}
        save={messageMetadataSave} onSave={setShowMessageMetadata} />
    </SettingRow>
    </>
  );
}

export function SessionsSection() {
  const dashboardTimeRangeDefault = useUiStore((s) => s.dashboardTimeRangeDefault);
  const setDashboardTimeRangeDefault = useUiStore((s) => s.setDashboardTimeRangeDefault);
  const sidebarRecentHours = useUiStore((s) => s.sidebarRecentHours);
  const setSidebarRecentHours = useUiStore((s) => s.setSidebarRecentHours);
  const timeRangeSave = useSettingSave();
  const recentSave = useSettingSave();
  const [autoArchive, setAutoArchive] = useState({ enabled: true, ttlDays: 7 });
  const [autoArchiveLoaded, setAutoArchiveLoaded] = useState(false);
  const autoArchiveToggleSave = useSettingSave();
  const autoArchiveTTLSave = useSettingSave();
  useEffect(() => {
    const ctrl = new AbortController();
    api
      .getAutoArchiveSettings(ctrl.signal)
      .then(setAutoArchive)
      .catch(() => { /* best-effort; keep defaults */ })
      .finally(() => {
        if (!ctrl.signal.aborted) setAutoArchiveLoaded(true);
      });
    return () => ctrl.abort();
  }, []);

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
      <ArchiveResurfaceSetting />
      <SnoozeSettings />
    </>
  );
}

function ArchiveResurfaceSetting() {
  const [mode, setMode] = useState('halt');
  const [loaded, setLoaded] = useState(false);
  const save = useSettingSave();
  useEffect(() => {
    const ctrl = new AbortController();
    api.getArchiveResurface(ctrl.signal).then((value) => {
      setMode(value.mode);
      setLoaded(true);
    }).catch(() => { /* Keep disabled until the saved preference is known. */ });
    return () => ctrl.abort();
  }, []);
  return (
    <SettingRow setting="archive-resurface">
      <SettingSelect
        ariaLabel="Show archived sessions again"
        placeholder="Session halts (default)"
        searchLabel="Search archive behavior"
        value={mode}
        options={[
          { value: 'activity', label: 'Any activity' },
          { value: 'halt', label: 'Session halts (default)' },
        ]}
        disabled={!loaded || save.state === 'saving'}
        save={save}
        onSave={async (next) => {
          const result = await api.setArchiveResurface(next);
          setMode(result.mode);
        }}
      />
    </SettingRow>
  );
}
