// Home Screen web apps on iOS/iPadOS ignore the default-browser setting and
// open external links in Safari. Chrome registers googlechrome(s)://, so
// rewriting an external link to that scheme opens it in Chrome instead.
// A web app cannot detect whether the scheme is installed, so this is opt-in.

const STORAGE_KEY = 'ocman:open-links-in-chrome'

type Nav = Pick<Navigator, 'userAgent' | 'maxTouchPoints'>

/** iPadOS 13+ reports a Mac user agent, so touch support tells them apart. */
export function isIOS(nav: Nav = navigator): boolean {
  return /iPad|iPhone|iPod/.test(nav.userAgent) ||
    (nav.userAgent.includes('Macintosh') && nav.maxTouchPoints > 1)
}

export function getOpenInChrome(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEY) === '1'
  } catch {
    return false
  }
}

export function setOpenInChrome(on: boolean): void {
  if (on) localStorage.setItem(STORAGE_KEY, '1')
  else localStorage.removeItem(STORAGE_KEY)
}

/**
 * chromeURL returns the Chrome-scheme URL for an external http(s) link, or
 * null when the link must be left alone (same origin, mailto:, tel:, …).
 * Other browsers (firefox://, microsoft-edge-https://) would map here.
 */
export function chromeURL(href: string, base: string): string | null {
  let url: URL
  try {
    url = new URL(href, base)
  } catch {
    return null
  }
  if (url.origin === new URL(base).origin) return null
  if (url.protocol === 'https:') return 'googlechromes:' + url.href.slice('https:'.length)
  if (url.protocol === 'http:') return 'googlechrome:' + url.href.slice('http:'.length)
  return null
}

/**
 * The rewritten URL when the setting applies on this device, else null.
 * ponytail: no window.open call sites exist today; route any new one
 * through this before opening.
 */
export function rewriteExternal(href: string): string | null {
  if (!isIOS() || !getOpenInChrome()) return null
  return chromeURL(href, location.href)
}

/**
 * Install the one delegated click listener. Call once at startup. It runs in
 * the capture phase so React handlers that stopPropagation (PR/Issue
 * open-in-browser icons, links inside dialogs) cannot hide the click.
 */
export function installExternalLinks(doc: Document = document): void {
  doc.addEventListener('click', (e) => {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    const a = (e.target as Element | null)?.closest?.('a[href]') as HTMLAnchorElement | null
    if (!a) return
    const chrome = rewriteExternal(a.href)
    if (!chrome) return
    e.preventDefault()
    location.href = chrome
  }, true)
}
