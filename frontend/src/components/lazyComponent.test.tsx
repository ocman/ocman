// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { lazyComponent } from './lazyComponent';

it('shows a loading state until the module resolves, then renders with props', async () => {
  let resolve!: (c: (props: { name: string }) => React.ReactNode) => void;
  const Greeting = lazyComponent<{ name: string }>(() => new Promise((r) => { resolve = r; }), 'Loading greeting…');
  render(<Greeting name="Ada" />);
  expect(screen.getByRole('status')).toHaveTextContent('Loading greeting…');
  resolve(({ name }) => <p>Hello {name}</p>);
  expect(await screen.findByText('Hello Ada')).toBeInTheDocument();
});
