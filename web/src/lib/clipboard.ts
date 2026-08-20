export function canWriteToClipboardNatively(): boolean {
  return Boolean(
    globalThis.isSecureContext && typeof globalThis.navigator?.clipboard?.writeText === 'function',
  )
}

export async function copyText(value: string): Promise<boolean> {
  const writeText = globalThis.navigator?.clipboard?.writeText
  if (canWriteToClipboardNatively() && typeof writeText === 'function') {
    try {
      await writeText.call(globalThis.navigator.clipboard, value)
      return true
    } catch {
      // 原生复制失败时继续使用兼容方式。
    }
  }

  const textarea = document.createElement('textarea')
  textarea.value = value
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.append(textarea)
  try {
    textarea.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    textarea.remove()
  }
}
