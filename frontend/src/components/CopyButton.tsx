import { useEffect, useRef, useState, type ComponentProps } from 'react';
import { copyTextToClipboard } from '../lib/clipboard';
import { Button } from './Control';
import { IconButton } from './IconButton';

type Props = Omit<ComponentProps<typeof Button>, 'children' | 'onClick' | 'aria-busy' | 'aria-label' | 'title'> & {
  text: string;
  label?: string;
  iconOnly?: boolean;
};
type Status = 'idle' | 'copying' | 'copied' | 'error';

export function CopyButton({ text, label = 'Copy', iconOnly = false, disabled, className = '', type = 'button', ...props }: Props) {
  const [feedback, setFeedback] = useState<{ text: string; status: Status }>({ text, status: 'idle' });
  const request = useRef(0);
  const status = feedback.text === text ? feedback.status : 'idle';

  useEffect(() => () => { request.current += 1; }, []);
  useEffect(() => {
    if (feedback.status !== 'copied') return;
    const timer = window.setTimeout(() => setFeedback({ text: feedback.text, status: 'idle' }), 2000);
    return () => window.clearTimeout(timer);
  }, [feedback]);

  const copy = async () => {
    const id = ++request.current;
    setFeedback({ text, status: 'copying' });
    const copied = await copyTextToClipboard(text);
    if (id === request.current) setFeedback({ text, status: copied ? 'copied' : 'error' });
  };
  const message = status === 'copied' ? 'Copied!' : status === 'error' ? 'Could not copy to clipboard. Try again.' : '';
  const icon = status === 'copied' ? 'bi-check2' : status === 'error' ? 'bi-exclamation-circle' : 'bi-copy';
  const buttonProps = {
    ...props,
    type,
    className: `oc-copy-button ${className}`,
    disabled: disabled || status === 'copying',
    'aria-busy': status === 'copying',
    'data-copy-state': status,
    title: message || label,
    onClick: () => { void copy(); },
  };

  return <>
    {iconOnly ? <IconButton {...buttonProps} label={label} icon={icon} /> :
      <Button {...buttonProps} aria-label={label}><i className={`bi ${icon}`} aria-hidden="true" />{status === 'copied' ? 'Copied!' : status === 'error' ? 'Copy failed' : label}</Button>}
    <span className="oc-copy-feedback" role="status">{message}</span>
  </>;
}
