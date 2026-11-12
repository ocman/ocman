import { expect, it } from 'vitest';
import type { PluginRegistration, PluginSetting } from './plugins';
import { initialPluginValues, pluginConfigurationErrors } from './pluginConfigurationValidation';

const registration = (setting: Partial<PluginSetting>, value?: unknown, configured = false): PluginRegistration => ({
  ownerId: 'local', approval: 'reviewed', checksum: 'fixture', enabled: false, removed: false, grants: [],
  description: { id: 'org.example.fixture', name: 'Fixture', version: '1', scope: 'owner', settings: [
    { key: 'value', label: 'Value', type: 'string', ...setting },
  ] },
  configuration: { values: value === undefined ? {} : { value } as unknown as PluginRegistration['configuration']['values'], secrets: { value: configured } },
  health: { status: 'disabled', restartCount: 0 },
});

it.each([
  [{ required: true }, undefined, 'Value is required.'],
  [{ required: true }, '', undefined], // Required means present, not a plugin-specific nonempty constraint.
  [{ type: 'boolean', required: true }, false, undefined],
  [{ type: 'integer', required: true }, 0, undefined],
  [{ type: 'integer' }, 1.5, 'Enter a whole number.'],
  [{ type: 'number' }, Infinity, 'Enter a finite number.'],
  [{ type: 'number' }, NaN, 'Enter a finite number.'],
  [{ type: 'number' }, 'private-raw-value', 'Enter a finite number.'],
  [{ type: 'boolean' }, 'false', 'Choose Yes or No.'],
  [{}, 1, 'Enter text.'],
  [{}, null, 'Enter text.'],
  [{ enum: ['declared'] }, 'private-raw-value', 'Choose one of the declared options.'],
  [{ enum: ['declared'] }, 'declared', undefined],
  [{ list: true }, ['one', 'two'], undefined],
  [{ list: true, required: true }, [], undefined],
  [{ list: true }, [1], 'Enter a list of text values.'],
  [{ list: true }, 'text', 'Enter a list of text values.'],
  [{ default: 'default', required: true }, undefined, undefined],
  [{}, undefined, undefined],
] as [Partial<PluginSetting>, unknown, string | undefined][])('matches the declared schema %j', (setting, value, error) => {
  expect(pluginConfigurationErrors(registration(setting, value))).toEqual(error ? { value: error } : {});
});

it('uses presence flags for unchanged secrets and checks only declared constraints on candidates', () => {
  const missing = registration({ secret: true, required: true });
  expect(pluginConfigurationErrors(missing)).toEqual({ value: 'Value is required and is not configured.' });
  const configured = registration({ secret: true, required: true }, undefined, true);
  expect(pluginConfigurationErrors(configured, { secrets: {} })).toEqual({});
  expect(pluginConfigurationErrors(configured, { secrets: { value: '' } })).toEqual({ value: 'Value is required and is not configured.' });
  expect(pluginConfigurationErrors(missing, { secrets: { value: 'any-nonempty-password' } })).toEqual({});
  expect(pluginConfigurationErrors(registration({ secret: true }), { secrets: { value: '' } })).toEqual({});
  expect(pluginConfigurationErrors(registration({ secret: true, enum: ['declared'] }), { secrets: { value: 'candidate' } }))
    .toEqual({ value: 'Choose one of the declared options.' });
});

it('applies defaults without replacing explicit false, zero, empty strings or lists', () => {
  expect(initialPluginValues(registration({ default: 'default' }))).toEqual({ value: 'default' });
  for (const value of [false, 0, '', []]) expect(initialPluginValues(registration({ default: 'default' }, value))).toEqual({ value });
  expect(initialPluginValues(registration({ secret: true }))).toEqual({});
});

it('does not treat inherited object properties as configured settings or secrets', () => {
  const plugin = registration({ key: 'constructor', required: true, default: 'default' });
  expect(initialPluginValues(plugin)).toEqual({ constructor: 'default' });
  expect(pluginConfigurationErrors(plugin)).toEqual({});
  plugin.description.settings![0] = { key: 'constructor', label: 'Value', type: 'string', required: true, secret: true };
  expect(pluginConfigurationErrors(plugin)).toEqual({ constructor: 'Value is required and is not configured.' });
});
