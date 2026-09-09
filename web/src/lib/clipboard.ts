export function canWriteToClipboardNatively(): boolean {
  return Boolean(
    globalThis.isSecureContext && typeof globalThis.navigator?.clipboard?.writeText === 'function',
  )
}

export async function copyText(
  value: string,
  target?: HTMLTextAreaElement,
  isCurrent: () => boolean = () => true,
): Promise<boolean> {
  if (!isCurrent()) return false
  const writeText = globalThis.navigator?.clipboard?.writeText
  if (canWriteToClipboardNatively() && typeof writeText === 'function') {
    try {
      await writeText.call(globalThis.navigator.clipboard, value)
      return true
    } catch {
      // 原生复制失败时继续使用兼容方式。
    }
  }

  // 原生接口等待期间可能已经关闭弹窗或切换账号，失效后不再尝试兼容复制。
  if (!isCurrent()) return false
  const activeElement = document.activeElement
  const textarea = target ?? document.createElement('textarea')
  try {
    if (target && !target.isConnected) return false
    textarea.value = value
    if (!target) {
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      // 保持在当前弹窗的焦点范围内，避免选中被焦点锁打断。
      const container =
        activeElement?.closest('[role="dialog"], [role="alertdialog"]') ?? document.body
      container.append(textarea)
    }
    textarea.focus({ preventScroll: true })
    textarea.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    if (!target) {
      textarea.remove()
      if (activeElement instanceof HTMLElement && activeElement.isConnected) {
        activeElement.focus({ preventScroll: true })
      }
    }
  }
}
