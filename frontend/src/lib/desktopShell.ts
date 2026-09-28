// The desktop build loads the backend's own http URL, so the Wails runtime
// script (served only on wails://) is absent. The native "external" message
// handler is still installed on the WebView, and it honours "drag" from any
// origin, so detecting it and posting "drag" keeps the title-bar behaviour.

type ExternalHandler = { postMessage: (message: string) => void }

function externalHandler(win: Window): ExternalHandler | undefined {
  const w = win as Window & { webkit?: { messageHandlers?: { external?: ExternalHandler } } }
  return w.webkit?.messageHandlers?.external
}

export function isDesktopShell(win: Window = window): boolean {
  return 'runtime' in win || externalHandler(win) !== undefined
}

// installDesktopShell tags <body> for desktop styles and, when the Wails
// runtime is not loaded, starts window drags from --wails-draggable: drag.
export function installDesktopShell(win: Window = window): void {
  if (!isDesktopShell(win)) return
  win.document.body.classList.add('wails-app')
  const external = externalHandler(win)
  if ('runtime' in win || !external) return
  // Wails' context-menu user script dereferences window.wails.flags.
  const w = win as Window & { wails?: { flags: Record<string, unknown> } }
  w.wails ??= { flags: {} }
  win.addEventListener('mousedown', (e) => {
    if (e.buttons !== 1 || e.detail !== 1 || !(e.target instanceof Element)) return
    const drag = win.getComputedStyle(e.target).getPropertyValue('--wails-draggable').trim()
    if (drag !== 'drag') return
    e.preventDefault()
    external.postMessage('drag')
  })
}
