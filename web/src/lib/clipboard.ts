function copyWithLegacyCommand(value: string): void {
  const previousFocus =
    document.activeElement instanceof HTMLElement ? document.activeElement : null
  const textarea = document.createElement('textarea')
  textarea.value = value
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.top = '0'
  textarea.style.left = '-9999px'
  textarea.style.opacity = '0'
  document.body.append(textarea)
  textarea.focus()
  textarea.select()

  try {
    if (!document.execCommand('copy')) throw new Error('legacy clipboard copy failed')
  } finally {
    textarea.remove()
    if (previousFocus?.isConnected) previousFocus.focus()
  }
}

export async function copyText(value: string): Promise<void> {
  const writeText = globalThis.navigator?.clipboard?.writeText
  if (typeof writeText === 'function') {
    try {
      await writeText.call(globalThis.navigator.clipboard, value)
      return
    } catch {
      // 原生剪贴板不可用时静默使用兼容方式。
    }
  }

  copyWithLegacyCommand(value)
}
