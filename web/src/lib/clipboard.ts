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

  const input = document.createElement('input')
  input.value = value
  input.style.position = 'fixed'
  input.style.opacity = '0'
  document.body.append(input)
  try {
    input.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    input.remove()
  }
}
