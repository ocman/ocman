// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { JudgeEndpointSettings } from './JudgeEndpointSettings';

const methods = vi.hoisted(() => ({ getJudgeEndpoint: vi.fn(), setJudgeEndpoint: vi.fn() }));
vi.mock('../../lib/api', () => ({ api: methods }));
const defaults = { format: '', endpoint: '', model: '', minSafeProbability: .99, apiKeySet: false };
const stored = { ...defaults, format: 'typesafe', endpoint: 'https://example.com/v1/systemone', model: 'local', apiKeySet: true };

describe('custom reviewer endpoint', () => {
  beforeEach(() => {
    methods.getJudgeEndpoint.mockReset().mockResolvedValue(defaults);
    methods.setJudgeEndpoint.mockReset().mockResolvedValue(stored);
  });

  it('loads the existing reviewer and saves a complete OpenAI endpoint', async () => {
    render(<JudgeEndpointSettings />);
    expect(screen.getByRole('status')).toHaveTextContent('Loading reviewer endpoint');
    fireEvent.change(await screen.findByLabelText('Reviewer API'), { target: { value: 'openai' } });
    fireEvent.change(screen.getByLabelText('Endpoint URL'), { target: { value: 'http://127.0.0.1:8080/v1/chat/completions' } });
    fireEvent.change(screen.getByLabelText('Model ID'), { target: { value: 'local-model' } });
    fireEvent.change(screen.getByLabelText('API key (optional)'), { target: { value: 'new-key' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save reviewer endpoint' }));
    await waitFor(() => expect(methods.setJudgeEndpoint).toHaveBeenCalledWith({ format: 'openai', endpoint: 'http://127.0.0.1:8080/v1/chat/completions', model: 'local-model', apiKey: 'new-key', minSafeProbability: .99 }));
    await waitFor(() => expect(screen.getByLabelText('API key (optional)')).toHaveValue(''));
  });

  it('preserves a stored key unless removal is requested', async () => {
    methods.getJudgeEndpoint.mockResolvedValue(stored);
    render(<JudgeEndpointSettings />);
    const key = await screen.findByLabelText('API key (optional)');
    expect(key).toHaveAttribute('type', 'password');
    expect(key).toHaveValue('');
    fireEvent.change(screen.getByLabelText('Minimum safe probability'), { target: { value: '.995' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save reviewer endpoint' }));
    await waitFor(() => expect(methods.setJudgeEndpoint).toHaveBeenCalledWith({ format: 'typesafe', endpoint: stored.endpoint, model: 'local', minSafeProbability: .995 }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save reviewer endpoint' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: 'Remove stored API key' }));
    expect(screen.getByText('API key will be removed when you save.')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Save reviewer endpoint' }));
    await waitFor(() => expect(methods.setJudgeEndpoint).toHaveBeenLastCalledWith({ format: 'typesafe', endpoint: stored.endpoint, model: 'local', minSafeProbability: .99, apiKey: '' }));
  });

  it('switches back to OpenCode without exposing custom endpoint fields', async () => {
    methods.getJudgeEndpoint.mockResolvedValue(stored);
    render(<JudgeEndpointSettings />);
    fireEvent.change(await screen.findByLabelText('Reviewer API'), { target: { value: '' } });
    expect(screen.queryByLabelText('Endpoint URL')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Save reviewer endpoint' }));
    await waitFor(() => expect(methods.setJudgeEndpoint).toHaveBeenCalledWith({ format: '', endpoint: stored.endpoint, model: 'local', minSafeProbability: .99 }));
  });

  it('shows load failures and retries without allowing an accidental overwrite', async () => {
    methods.getJudgeEndpoint.mockRejectedValueOnce(new Error('Unavailable')).mockResolvedValueOnce(defaults);
    render(<JudgeEndpointSettings />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Unavailable');
    expect(screen.queryByRole('button', { name: 'Save reviewer endpoint' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByLabelText('Reviewer API')).toHaveValue('');
  });

  it('shows save errors and retains the entered key for retry', async () => {
    methods.getJudgeEndpoint.mockResolvedValue(stored);
    methods.setJudgeEndpoint.mockRejectedValue(new Error('Invalid endpoint'));
    render(<JudgeEndpointSettings />);
    fireEvent.change(await screen.findByLabelText('API key (optional)'), { target: { value: 'replacement' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save reviewer endpoint' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Invalid endpoint');
    expect(screen.getByLabelText('API key (optional)')).toHaveValue('replacement');
  });

  it('aborts the settings request when unmounted', async () => {
    const { unmount } = render(<JudgeEndpointSettings />);
    const signal = methods.getJudgeEndpoint.mock.calls[0][0] as AbortSignal;
    unmount();
    expect(signal.aborted).toBe(true);
  });
});
