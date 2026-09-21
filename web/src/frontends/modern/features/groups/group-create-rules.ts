import type { GroupChannel, ModelDraft } from '@modern/api/group-create'

export interface GroupDraftModel extends ModelDraft {
  key: number
  origin: 'manual' | 'discovery' | 'configured'
}
export function modelErrors(models: readonly GroupDraftModel[]): Map<number, 'id' | 'duplicate'> {
  const counts = new Map<string, number>()
  for (const model of models) {
    const name = JSON.stringify([model.id.trim(), model.alias.trim() || model.id.trim()])
    counts.set(name, (counts.get(name) ?? 0) + 1)
  }
  const errors = new Map<number, 'id' | 'duplicate'>()
  for (const model of models) {
    if (!model.id.trim()) errors.set(model.key, 'id')
    else if (
      (counts.get(JSON.stringify([model.id.trim(), model.alias.trim() || model.id.trim()])) ?? 0) >
      1
    )
      errors.set(model.key, 'duplicate')
  }
  return errors
}

export function credentialCount(raw: string, channel: GroupChannel | undefined): number {
  const value = raw.trim()
  if (!value) return 0
  if (
    channel &&
    (channel.credentialFields.length !== 1 || channel.credentialFields[0]?.key !== 'api_key') &&
    value.startsWith('{')
  ) {
    try {
      const parsed: unknown = JSON.parse(value)
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) return 1
    } catch {
      /* 多行 JSON 凭据继续按行统计，完整校验交给后端。 */
    }
  }
  return value.split(/\r?\n/u).filter((line) => line.trim()).length
}
export function validBaseURL(value: string): boolean {
  try {
    const url = new URL(value)
    return (
      ['http:', 'https:'].includes(url.protocol) &&
      Boolean(url.hostname) &&
      !url.username &&
      !url.password &&
      !url.search &&
      !url.hash &&
      !value.endsWith('?')
    )
  } catch {
    return false
  }
}
