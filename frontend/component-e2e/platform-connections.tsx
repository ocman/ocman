// Browser-only component fixture. No backend, native agent, or real account is used.
import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import 'bootstrap-icons/font/bootstrap-icons.css';
import '../src/tokens.css';
import '../src/base.css';
import '../src/shared.css';
import './review.css';
import { ConfirmProvider } from '../src/components/ConfirmDialog';
import { PlatformConnections } from '../src/components/PlatformConnections';
import type { ConnectionCheck, ConnectionInput, ConnectionPlatform, PlatformConnection } from '../src/lib/platformConnections';

const platforms: ConnectionPlatform[] = [
  { id: 'opencode', label: 'OpenCode', existingDirectory: '/home/user/.config/opencode', supportsIsolation: true, isolationNote: 'Ocman will use separate OpenCode configuration, credentials, data and a dedicated server.' },
  { id: 'claudecode', label: 'Claude Code', existingDirectory: '/home/user/.claude', supportsIsolation: true, isolationNote: 'Ocman will use a separate Claude configuration and supervisor.' },
  { id: 'codex', label: 'Codex', existingDirectory: '/home/user/.codex', supportsIsolation: true, isolationNote: 'Ocman will use a separate Codex home and supervisor.' },
];
const fixtures: PlatformConnection[] = [
  { id: 'pc-claude-personal', kind: 'claudecode', name: 'Claude Personal', configuration: 'existing', directory: '/home/user/.claude', enabled: true, isDefault: true, status: 'ready', account: 'personal@example.com' },
  { id: 'pc-claude-work', kind: 'claudecode', name: 'Claude Work', configuration: 'isolated', directory: '/ocman/connections/claude-work', enabled: true, isDefault: false, status: 'ready', account: 'work@example.com' },
  { id: 'pc-opencode-work', kind: 'opencode', name: 'OpenCode Work', configuration: 'isolated', directory: '/ocman/connections/opencode-work', enabled: true, isDefault: false, status: 'ready', account: 'work@example.com' },
  { id: 'pc-codex-personal', kind: 'codex', name: 'Codex Personal', configuration: 'isolated', directory: '/ocman/connections/codex-personal', enabled: true, isDefault: false, status: 'sign-in-required' },
];
const params = new URLSearchParams(location.search);
const onboarding = params.has('onboarding');
const storageKey = `connection-ui-review:${onboarding ? 'onboarding' : 'settings'}`;

export function Review() {
  const [connections, setConnections] = useState<PlatformConnection[]>(() => {
    const saved = localStorage.getItem(storageKey);
    return saved ? JSON.parse(saved) as PlatformConnection[] : onboarding ? [] : fixtures;
  });
  const [nextStep, setNextStep] = useState('');
  function persist(values: PlatformConnection[]) {
    localStorage.setItem(storageKey, JSON.stringify(values));
    setConnections(values);
  }
  async function check(input: ConnectionInput): Promise<ConnectionCheck> {
    if (params.has('check-error')) throw new Error('The account check failed. Your connection has not been changed.');
    const status = params.has('sign-in') ? 'sign-in-required' : 'ready';
    return {
      status,
      directory: input.directory || '/ocman/connections/new-account',
      account: status === 'ready' ? 'work@example.com' : undefined,
      checks: [
        { label: 'Coding tool', ok: true, detail: 'Installed on this machine.' },
        { label: 'Configuration', ok: true, detail: 'Separate configuration selected; existing credentials are not copied.' },
        { label: 'Account', ok: status === 'ready', detail: status === 'ready' ? 'Signed in to work@example.com.' : 'Sign in to this configuration to finish setup.' },
      ],
      instructions: status === 'ready' ? undefined : 'Run this command on this machine, then choose Check again.',
      command: status === 'ready' ? undefined : 'CODEX_HOME=/ocman/connections/new-account codex login',
    };
  }
  async function save(input: ConnectionInput, id?: string) {
    if (params.has('save-error')) throw new Error('Could not save platform connection. Please retry.');
    if (id) persist(connections.map(value => value.id === id ? { ...value, name: input.name, enabled: input.enabled } : value));
    else {
      const result = await check(input);
      persist([...connections, { ...input, id: crypto.randomUUID(), isDefault: connections.length === 0, directory: result.directory ?? '', status: result.status, account: result.account }]);
    }
  }
  return <>
    <header className="review-header"><strong>ocman</strong><span>UI review · {onboarding ? 'Getting started' : 'Settings / Platforms'}</span></header>
    <main className="review-main">
      <h1>{onboarding ? 'Welcome to Ocman' : 'Platforms'}</h1>
      <p className="review-description">{onboarding ? 'Connect your coding tools, choose a project, and start a conversation.' : 'Manage the coding tools and accounts available on this machine.'}</p>
      <PlatformConnections connections={connections} platforms={platforms} machineName="This machine" onboarding={onboarding}
        onRetry={() => {}} onCheck={check} onSave={save}
        onDefault={async id => persist(connections.map(value => ({ ...value, isDefault: value.id === id })))}
        onEnabledChange={async (id, enabled) => persist(connections.map(value => value.id === id ? { ...value, enabled } : value))}
        onRemove={async id => persist(connections.filter(value => value.id !== id))}
        onSkip={() => setNextStep('Setup skipped. You can connect a platform from Settings.')}
        onContinue={() => setNextStep('Choose a project directory')} />
      {nextStep && <p role="status">{nextStep}</p>}
      <footer className="review-footer">Component review with fixture accounts. No real credentials or agents are used.</footer>
    </main>
  </>;
}

createRoot(document.getElementById('root')!).render(<BrowserRouter><ConfirmProvider><Review /></ConfirmProvider></BrowserRouter>);
