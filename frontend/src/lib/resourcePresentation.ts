import type { ResourceProcess } from './resources';

export function processKey(process: ResourceProcess) {
  return `${process.pid}:${process.parentPid}:${process.name}`;
}

const colors = ['--accent', '--accent2', '--accent3', '--accent4', '--teal', '--pink', '--yellow', '--lavender'];
export function processColor(process: ResourceProcess) {
  return `var(${colors[process.pid % colors.length]})`;
}

export function formatMemory(bytes: number) {
  return bytes >= 1024 ** 3 ? `${(bytes / 1024 ** 3).toFixed(2)} GiB` : `${(bytes / 1024 ** 2).toFixed(1)} MiB`;
}
