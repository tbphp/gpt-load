import type { AccessProtocol, GroupSummary } from '@/api/control/types'
import { enabledDataProtocols } from '@/api/control/protocols'

export function accessKeyProtocolOptions(
  editingBaseProtocols: readonly AccessProtocol[] = [],
): AccessProtocol[] {
  return editingBaseProtocols.includes('openai-response')
    ? [...enabledDataProtocols, 'openai-response']
    : [...enabledDataProtocols]
}

export function buildAccessKeyModelOptions(
  groups: GroupSummary[],
  preserved: string[] = [],
): string[] {
  const values: string[] = []
  for (const group of groups) {
    for (const model of group.models) {
      values.push(model.id, model.alias)
    }
  }
  values.push(...preserved)
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))]
}
