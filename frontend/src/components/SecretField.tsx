import { useState, type ComponentPropsWithRef } from 'react';
import { TextField } from './Control';
import { IconButton } from './IconButton';

/** A masked text field with an eye toggle to inspect the value. */
export function SecretField({ disabled, ...props }: Omit<ComponentPropsWithRef<'input'>, 'type'>) {
  const [shown, setShown] = useState(false);
  return (
    <span className="oc-secret-field">
      <TextField {...props} disabled={disabled} type={shown ? 'text' : 'password'} autoComplete="off" />
      <IconButton icon={shown ? 'bi-eye-slash' : 'bi-eye'} label={shown ? 'Hide secret' : 'Show secret'} variant="ghost" disabled={disabled} aria-pressed={shown} onClick={() => setShown(!shown)} />
    </span>
  );
}
