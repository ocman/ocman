import { useState } from 'react';
import { plugins, type PluginInput, type PluginMutation, type PluginRegistration } from './plugins';
import { APIError } from './api.requests';
import { pluginConfigurationErrors } from './pluginConfigurationValidation';

export function usePluginManagement(plugin: PluginRegistration, owner: string, refresh: () => Promise<void>, onConfigure?: () => void) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [review, setReview] = useState<'enable' | 'grants' | null>(null);
  const [configurationAttempt, setConfigurationAttempt] = useState(0);
  const [configurationSaved, setConfigurationSaved] = useState(false);
  const [configurationFocus, setConfigurationFocus] = useState(0);
  const [configurationRequested, setConfigurationRequested] = useState(false);
  const [enableNeedsConfiguration, setEnableNeedsConfiguration] = useState(false);
  const [pendingAction, setPendingAction] = useState<PluginMutation | null>(null);
  const [remove, setRemove] = useState(false);

  function requestConfiguration() {
    setConfigurationRequested(true);
    setReview(null);
    setConfigurationSaved(false);
    setConfigurationFocus((attempt) => attempt + 1);
    onConfigure?.();
  }

  function needsConfiguration(input?: PluginInput) {
    if (!Object.keys(pluginConfigurationErrors(plugin, input)).length) return false;
    requestConfiguration();
    return true;
  }

  function reviewEnable() {
    if (busy) return false;
    if (needsConfiguration()) { setEnableNeedsConfiguration(true); return false; }
    setReview('enable');
    return true;
  }

  async function mutate(action: PluginMutation, input?: PluginInput) {
    if (busy) return;
    if (action === 'enable' && needsConfiguration()) { setEnableNeedsConfiguration(true); return; }
    if (action === 'configuration' && needsConfiguration(input)) return;
    setBusy(true);
    setPendingAction(action);
    setError('');
    setConfigurationSaved(false);
    try {
      await plugins.mutate(owner, plugin.description.id, action, input);
      if (action === 'configuration') { setConfigurationSaved(true); setEnableNeedsConfiguration(false); setConfigurationAttempt((attempt) => attempt + 1); }
      setReview(null); setRemove(false);
    } catch (failure) {
      // A response body can contain submitted configuration or secrets.
      const validationRejected = failure instanceof APIError && failure.status === 400;
      let message = `Could not ${action === 'grants' ? 'update grants for' : action} plugin. Check its health and retry.`;
      if (action === 'configuration') message = validationRejected
        ? 'Configuration was rejected. Review the declared settings and saved values before retrying.'
        : 'Could not confirm configuration was saved and activated. After activation failure, the server attempts to restore the last working configuration. Review saved values and plugin health before retrying.';
      if (action === 'enable' && validationRejected) {
        message = 'Could not enable plugin. The owner rejected its configuration or grants. Review settings and permissions before retrying.';
        requestConfiguration();
      }
      setError(message);
      if (action === 'configuration') setConfigurationAttempt((attempt) => attempt + 1);
    } finally {
      try { await refresh(); }
      finally { setBusy(false); setPendingAction(null); }
    }
  }
  return { busy, error, review, setReview, reviewEnable, configurationAttempt, configurationSaved, configurationFocus, configurationRequested,
    enableNeedsConfiguration, pendingAction, remove, setRemove, mutate };
}

export type PluginManagement = ReturnType<typeof usePluginManagement>;
