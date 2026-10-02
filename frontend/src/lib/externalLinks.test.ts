// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest'
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
  afterEach(() => { localStorage.clear(); document.body.innerHTML = '' })

  it('ignores the saved setting off iOS', () => {
    // jsdom is not iOS, so a restored "on" must not intercept the click.
    setOpenInChrome(true)
    installExternalLinks()
    document.body.innerHTML = '<a href="https://github.com/">x</a>'
    let prevented: boolean | undefined
    // Record, then cancel, so jsdom does not try to navigate.
    const stop = (e: Event) => { prevented = e.defaultPrevented; e.preventDefault() }
    window.addEventListener('click', stop)
    document.querySelector('a')!.click()
    window.removeEventListener('click', stop)
    expect(prevented).toBe(false)
  })
})
