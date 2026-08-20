export function canWriteToClipboardNatively(): boolean {
  return typeof globalThis.navigator?.clipboard?.writeText === 'function'
}

export async function copyText(value: string): Promise<void> {
  const writeText = globalThis.navigator?.clipboard?.writeText
  if (typeof writeText !== 'function') throw new Error('clipboard unavailable')

  await writeText.call(globalThis.navigator.clipboard, value)
}
