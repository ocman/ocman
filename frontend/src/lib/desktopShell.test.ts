// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { installDesktopShell, isDesktopShell } from './desktopShell'

type TestWindow = Window & { webkit?: unknown; runtime?: unknown; wails?: unknown }

function withExternal(postMessage = vi.fn()) {
  ;(window as TestWindow).webkit = { messageHandlers: { external: { postMessage } } }
  return postMessage
}

afterEach(() => {
  delete (window as TestWindow).webkit
  delete (window as TestWindow).runtime
  delete (window as TestWindow).wails
  document.body.className = ''
  document.body.innerHTML = ''
})

describe('desktopShell', () => {
  it('is inert in a plain browser', () => {
    expect(isDesktopShell()).toBe(false)
    installDesktopShell()
    expect(document.body.classList.contains('wails-app')).toBe(false)
  })

  it('tags the body when the Wails runtime is loaded', () => {
    ;(window as TestWindow).runtime = {}
    installDesktopShell()
    expect(document.body.classList.contains('wails-app')).toBe(true)
  })

  it('posts drag from draggable regions over the native handler', () => {
    const postMessage = withExternal()
    const target = window as Window
    installDesktopShell(target)
    expect(document.body.classList.contains('wails-app')).toBe(true)
    expect((window as TestWindow).wails).toEqual({ flags: {} })

    const header = document.createElement('div')
    header.style.setProperty('--wails-draggable', 'drag')
    const button = document.createElement('button')
    document.body.append(header, button)

    button.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, buttons: 1, detail: 1 }))
    header.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, buttons: 1, detail: 2 }))
    expect(postMessage).not.toHaveBeenCalled()

    header.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, buttons: 1, detail: 1 }))
    expect(postMessage).toHaveBeenCalledWith('drag')
  })
})
