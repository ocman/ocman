// @vitest-environment jsdom
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { chromeURL, installExternalLinks, isIOS, setOpenInChrome } from './externalLinks'

const BASE = 'https://ocman.local:8228/sessions'
const MAC = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15'

describe('chromeURL', () => {
  it.each([
    ['https://github.com/a/b?x=1#c', 'googlechromes://github.com/a/b?x=1#c'],
    ['http://example.com/', 'googlechrome://example.com/'],
    ['https://ocman.local:8228/settings', null], // same origin
    ['/settings', null],
    ['#top', null],
    ['mailto:a@b.c', null],
    ['tel:+3212345', null],
  ])('%s → %s', (href, want) => {
    expect(chromeURL(href, BASE)).toBe(want)
  })
})

describe('isIOS', () => {
  it.each([
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)', 0, true],
    ['Mozilla/5.0 (iPad; CPU OS 12_0 like Mac OS X)', 5, true],
    [MAC, 5, true], // iPadOS 13+
    [MAC, 0, false], // desktop Mac
  ])('%s touch=%d → %s', (userAgent, maxTouchPoints, want) => {
    expect(isIOS({ userAgent, maxTouchPoints })).toBe(want)
  })
})

describe('installExternalLinks', () => {
  // Same origin as jsdom's document, so relative hrefs resolve consistently.
  const HERE = window.location.href
  const loc = { href: HERE }
  beforeAll(() => installExternalLinks())
  beforeEach(() => {
    loc.href = HERE
    vi.stubGlobal('location', loc)
    setOpenInChrome(true)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    localStorage.clear()
    document.body.innerHTML = ''
  })

  const asIOS = () => vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue('Mozilla/5.0 (iPhone)')

  // Dispatches a click on the link; returns whether the listener cancelled it.
  function click(html: string, init: MouseEventInit = {}): boolean {
    document.body.innerHTML = html
    const ev = new MouseEvent('click', { bubbles: true, cancelable: true, ...init })
    // Cancel at the target as well so jsdom never tries to navigate.
    const a = document.querySelector('a')!
    let prevented = false
    a.addEventListener('click', (e) => { prevented = e.defaultPrevented; e.preventDefault() })
    a.dispatchEvent(ev)
    return prevented
  }

  it('ignores the saved setting off iOS', () => {
    expect(click('<a href="https://github.com/">x</a>')).toBe(false)
    expect(loc.href).toBe(HERE)
  })

  it('opens external links in Chrome even when an ancestor stops propagation', () => {
    asIOS()
    document.body.innerHTML = '<div id="stop"><a href="https://github.com/a">x</a></div>'
    document.getElementById('stop')!.addEventListener('click', (e) => e.stopPropagation())
    const a = document.querySelector('a')!
    const ev = new MouseEvent('click', { bubbles: true, cancelable: true })
    a.addEventListener('click', (e) => e.preventDefault())
    a.dispatchEvent(ev)
    expect(loc.href).toBe('googlechromes://github.com/a')
  })

  it('leaves modifier clicks and same-origin links alone on iOS', () => {
    asIOS()
    expect(click('<a href="https://github.com/">x</a>', { metaKey: true })).toBe(false)
    expect(click('<a href="/settings">x</a>')).toBe(false)
    expect(loc.href).toBe(HERE)
  })
})
