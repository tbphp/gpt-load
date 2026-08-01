import type { AccessProtocol, GroupOptionDto } from '@/api/control/types'
import { enabledDataProtocols } from '@/api/control/protocols'

export function accessKeyProtocolOptions(): AccessProtocol[] {
  return [...enabledDataProtocols]
}

export function buildAccessKeyModelOptions(
  groups: GroupOptionDto[],
  preserved: string[] = [],
): string[] {
  const values: string[] = []
  for (const group of groups) {
    values.push(...group.models)
  }
  values.push(...preserved)
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))]
}
