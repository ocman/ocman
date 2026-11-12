import type { PluginInput, PluginRegistration, PluginSetting } from './plugins';

export function initialPluginValues(plugin: PluginRegistration): NonNullable<PluginInput['values']> {
  const values = { ...plugin.configuration.values };
  for (const setting of plugin.description.settings ?? []) {
    if (!setting.secret && (!Object.hasOwn(values, setting.key) || values[setting.key] === undefined) && setting.default !== undefined) values[setting.key] = setting.default;
  }
  return values;
}

// Mirrors protocol.Setting.Accepts and the management required/presence checks.
// This checks declarations only, not credentials or plugin-specific constraints.
function valueError(setting: PluginSetting, value: unknown): string | undefined {
  if (setting.list) return Array.isArray(value) && value.every((item) => typeof item === 'string') ? undefined : 'Enter a list of text values.';
  switch (setting.type) {
    case 'string':
      if (typeof value !== 'string') return 'Enter text.';
      return setting.enum?.length && !setting.enum.includes(value) ? 'Choose one of the declared options.' : undefined;
    case 'boolean': return typeof value === 'boolean' ? undefined : 'Choose Yes or No.';
    case 'number':
    case 'integer':
      if (typeof value !== 'number' || !Number.isFinite(value)) return 'Enter a finite number.';
      return setting.type === 'integer' && !Number.isInteger(value) ? 'Enter a whole number.' : undefined;
  }
}

export function pluginConfigurationErrors(plugin: PluginRegistration, input: PluginInput = { values: plugin.configuration.values ?? {} }): Record<string, string> {
  const errors: Record<string, string> = Object.create(null);
  for (const setting of plugin.description.settings ?? []) {
    let reason: string | undefined;
    if (setting.secret) {
      const candidate = input.secrets && Object.hasOwn(input.secrets, setting.key) ? input.secrets[setting.key] : undefined;
      const stored = plugin.configuration.secrets;
      const present = candidate === undefined ? stored && Object.hasOwn(stored, setting.key) && stored[setting.key] : candidate !== '';
      if (setting.required && !present) reason = `${setting.label} is required and is not configured.`;
      else if (candidate) reason = valueError(setting, candidate);
    } else {
      const supplied = input.values && Object.hasOwn(input.values, setting.key) ? input.values[setting.key] : undefined;
      const value = supplied === undefined ? setting.default : supplied;
      if (value === undefined) {
        if (setting.required) reason = `${setting.label} is required.`;
      } else reason = valueError(setting, value);
    }
    if (reason) errors[setting.key] = reason;
  }
  return errors;
}
