// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { ModelLabel } from './ModelLogo';

it('renders a decorative model logo before the model label', () => {
  render(<ModelLabel model="opencode/claude-opus-4-8">Claude Opus 4.8</ModelLabel>);

  expect(screen.getByText('Claude Opus 4.8')).toHaveTextContent('Claude Opus 4.8');
  expect(document.querySelector('img')).toHaveAttribute('title', 'Claude');
  expect(document.querySelector('img')).toHaveAttribute('alt', '');
  expect(document.querySelector('img')).not.toHaveClass('oc-model-logo--mono');
});

it('lightens a monochrome fallback logo', () => {
  render(<ModelLabel model="anthropic/custom-model">Custom model</ModelLabel>);

  expect(document.querySelector('img')).toHaveClass('oc-model-logo--mono');
});

it('can force a colored logo to render monochrome', () => {
  render(<ModelLabel model="anthropic/claude-opus-4" monochrome>Claude Opus 4</ModelLabel>);

  expect(document.querySelector('img')).toHaveClass('oc-model-logo--mono');
});
