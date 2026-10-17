import { useState } from 'react';
import { Button } from '../../components/Control';
import { SettingRow, SettingToggle } from '../../components/SettingRow';
import { DefaultAgentSetting } from '../../components/DefaultAgentSetting';
import { PromptTemplateSettings } from '../../components/upstream/PromptTemplateSettings';
import { RemoteSettings } from '../../components/RemoteSettings';
import { getOpenInChrome, isIOS, setOpenInChrome } from '../../lib/externalLinks';
import { usePwaInstall } from '../../lib/usePwaInstall';
import { useSettingSave } from '../../lib/useSaveStatus';
import { useAuthStore } from '../../lib/authStore';
import { LinkPreviewTabs } from '../../components/LinkPreviewTabs';
import { settingEntry } from '../../lib/settingsCatalog';
import type { RevealRequest } from '../../lib/useRevealSetting';

export function LinkPreviewSection({ target }: { target?: RevealRequest | null }) {
  return <LinkPreviewTabs key={target?.seq ?? ''} tab={target ? settingEntry(target.id).tab : undefined} />;
}

export function NewSessionSettings() {
  return <>
    <DefaultAgentSetting />
    <SettingRow block setting="launch-prompt-templates"><PromptTemplateSettings /></SettingRow>
  </>;
}

export function RemoteSection() {
  return <SettingRow block setting="remotes"><RemoteSettings /></SettingRow>;
}

export function DeviceSettings() {
  const { canInstall, installed, promptInstall } = usePwaInstall();
  const [openInChrome, setOpenInChromeState] = useState(getOpenInChrome);
  const chromeSave = useSettingSave();
  return <>
    {isIOS() && <SettingRow setting="open-links-in-chrome">
      <SettingToggle ariaLabel="Open external links in Chrome" checked={openInChrome} save={chromeSave}
        onSave={(next) => { setOpenInChrome(next); setOpenInChromeState(next); }} />
    </SettingRow>}
    {(canInstall || installed) && <SettingRow setting="install-app"
      desc={installed ? 'ocman is installed as an app on this device. Launch it from your dock or app launcher to use it in its own window.' : undefined}>
      <Button type="button" size="small" disabled={installed || !canInstall}
        onClick={() => { void promptInstall(); }}>{installed ? 'Installed' : 'Install'}</Button>
    </SettingRow>}
  </>;
}

export function AccountSettings() {
  const logout = useAuthStore((s) => s.logout);
  return <SettingRow setting="sign-out">
    <Button type="button" size="small" onClick={() => { void logout(); }}>Sign out</Button>
  </SettingRow>;
}
