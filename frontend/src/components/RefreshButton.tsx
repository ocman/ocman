import { IconButton, type IconButtonProps } from './IconButton';

type RefreshButtonProps = Omit<IconButtonProps, 'icon' | 'label' | 'aria-busy'> & {
  label?: string;
  loading?: boolean;
};

export function RefreshButton({ loading = false, disabled = false, label = 'Refresh', size = 'compact', variant = 'ghost', ...props }: RefreshButtonProps) {
  return <IconButton {...props} icon="bi-arrow-clockwise" label={label} size={size} variant={variant} aria-busy={loading} disabled={disabled || loading} />;
}
