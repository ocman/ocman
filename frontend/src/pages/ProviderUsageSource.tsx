export function ProviderUsageSource({ platformName, machineName }: { platformName?: string; machineName?: string }) {
  const label = [platformName, machineName].filter(Boolean).join(' · ');
  return label ? <p className="subscription-source">{label}</p> : null;
}
