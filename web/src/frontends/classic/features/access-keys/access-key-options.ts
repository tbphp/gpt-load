import type { AccessProtocol, GroupOptionDto } from '@/api/control/types'
import { enabledDataProtocols } from '@/api/control/protocols'
import type { ChannelDto } from '@/app/resources/channels'

export function accessKeyProtocolOptions(): AccessProtocol[] {
  return [...enabledDataProtocols]
}

export function buildAccessKeyProtocolCandidates(
  groups: GroupOptionDto[],
  channels: ChannelDto[],
  selectedGroupIDs: readonly number[] = [],
): AccessProtocol[] {
  const selected = selectAccessKeyGroups(groups, selectedGroupIDs)
  const supported = new Set<AccessProtocol>()
  const channelByID = new Map(channels.map((channel) => [channel.channel_id, channel]))
  for (const group of selected) {
    for (const protocol of channelByID.get(group.channel_id)?.client_protocols ?? []) {
      supported.add(protocol)
    }
  }
  return enabledDataProtocols.filter((protocol) => supported.has(protocol))
}

export function buildAccessKeyModelOptions(
  groups: GroupOptionDto[],
  preserved: string[] = [],
  selectedGroupIDs: readonly number[] = [],
): string[] {
  const values: string[] = []
  for (const group of selectAccessKeyGroups(groups, selectedGroupIDs)) {
    values.push(...group.models)
  }
  values.push(...preserved)
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))]
}

function selectAccessKeyGroups(
  groups: GroupOptionDto[],
  selectedGroupIDs: readonly number[],
): GroupOptionDto[] {
  if (selectedGroupIDs.length === 0) return groups
  const selected = new Set(selectedGroupIDs)
  return groups.filter(({ id }) => selected.has(id))
}
