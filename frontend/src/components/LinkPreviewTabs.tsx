import { LinkPreviewSettings } from './LinkPreviewSettings';
import { PreviewAppSettings } from './PreviewAppSettings';
import { PreviewProviderSettings } from './PreviewProviderSettings';
import { SettingRow } from './SettingRow';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './Tabs';

/** Settings → Link previews, split into accounts, sign-in apps and link rules. */
export function LinkPreviewTabs() {
  return (
    <Tabs defaultValue="accounts">
      <TabsList aria-label="Link preview settings">
        <TabsTrigger value="accounts">Accounts</TabsTrigger>
        <TabsTrigger value="apps">Sign-in apps</TabsTrigger>
        <TabsTrigger value="rules">Link rules</TabsTrigger>
      </TabsList>
      <TabsContent value="accounts">
        <p className="settings-row-desc">Where previews come from, and the accounts this browser has connected.</p>
        <PreviewProviderSettings />
      </TabsContent>
      <TabsContent value="apps">
        <p className="settings-row-desc">OAuth apps that let viewers connect their own accounts. Saved apps override the environment.</p>
        <PreviewAppSettings />
      </TabsContent>
      <TabsContent value="rules">
        <SettingRow block label="Custom link rules" desc="Create link cards for ticket IDs and other text patterns.">
          <LinkPreviewSettings />
        </SettingRow>
      </TabsContent>
    </Tabs>
  );
}
