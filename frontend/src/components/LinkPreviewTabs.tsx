import { LinkPreviewSettings } from './LinkPreviewSettings';
import { PreviewAppSettings } from './PreviewAppSettings';
import { PreviewProviderSettings } from './PreviewProviderSettings';
import { SettingRow } from './SettingRow';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './Tabs';

/** Settings → Link previews, split into accounts, sign-in apps and link rules. */
export function LinkPreviewTabs({ tab = 'accounts' }: { tab?: 'accounts' | 'apps' | 'rules' }) {
  return (
    <Tabs defaultValue={tab}>
      <TabsList aria-label="Link preview settings">
        <TabsTrigger value="accounts">Providers</TabsTrigger>
        <TabsTrigger value="apps">Sign-in apps</TabsTrigger>
        <TabsTrigger value="rules">Link rules</TabsTrigger>
      </TabsList>
      <TabsContent value="accounts">
        <p className="settings-row-desc">Previews are fetched on this machine with its tokens. Only links on set-up providers are looked up.</p>
        <PreviewProviderSettings />
      </TabsContent>
      <TabsContent value="apps">
        <p className="settings-row-desc">Optional. Needed only for Slack and Jira, which have no personal tokens. Saved apps override the environment.</p>
        <PreviewAppSettings />
      </TabsContent>
      <TabsContent value="rules">
        <SettingRow block setting="custom-link-rules">
          <LinkPreviewSettings />
        </SettingRow>
      </TabsContent>
    </Tabs>
  );
}
