import type { AccessProtocol, GroupSummary } from '@/api/control/types'

export const accessKeyProtocols: AccessProtocol[] = [
  'openai',
  'anthropic',
  'gemini',
  'openai-response',
]

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
