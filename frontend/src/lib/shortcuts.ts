export function isMacPlatform(): boolean {
  return /Mac|iPhone|iPad/.test(navigator.platform);
}

export function shortcutLabel(windowsLabel: string, macLabel = windowsLabel): string {
  return isMacPlatform() ? macLabel : windowsLabel;
}

export function openVSCode(directory: string) {
  window.location.href = `vscode://file${directory}`;
}

export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tagName = target.tagName;
  return target.isContentEditable || tagName === 'INPUT' || tagName === 'TEXTAREA' || tagName === 'SELECT';
}
