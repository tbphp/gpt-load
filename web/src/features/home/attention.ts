import type { HealthProblemCredentialDto, HealthQuotaCredentialDto } from '@/app/resources/health'

export type AttentionKind = 'blacklisted' | 'lowQuota'

export interface AttentionItem {
  kind: AttentionKind
  groupID: number
  groupName: string
  /** blacklisted：该分组被拉黑的凭据数；lowQuota：剩余额度比例 0..1 */
  value: number
  /** 仅 lowQuota：额度窗口重置时刻 */
  resetAtMS?: number
}

/** 首页最多渲染的条数；超过就退化成一行汇总，避免把接入面板挤到折叠线以下。 */
export const attentionRowLimit = 2

/**
 * 把健康响应压成首页「需要处理」要说的话。
 *
 * 刻意不包含冷却：冷却几分钟内自愈，看完不需要做任何事，
 * 而且那句「最早 N 分钟后恢复」是监控页健康条的原文，搬过来就是两处维护同一份文案。
 *
 * 也刻意不说「全部拉黑」：classifyHealthKey 把 auth_state != ready 的凭据归入 disabled 桶，
 * 且 disabled 不计入 counts.credentials，拿它当分母会算错。只报绝对条数。
 */
export function collectAttentionItems(
  blacklisted: readonly HealthProblemCredentialDto[],
  lowQuota: readonly HealthQuotaCredentialDto[],
): AttentionItem[] {
  const byGroup = new Map<number, AttentionItem>()
  for (const credential of blacklisted) {
    const existing = byGroup.get(credential.group_id)
    if (existing) {
      existing.value += 1
      continue
    }
    byGroup.set(credential.group_id, {
      kind: 'blacklisted',
      groupID: credential.group_id,
      groupName: credential.group_name,
      value: 1,
    })
  }

  const items = [...byGroup.values()].sort((left, right) => right.value - left.value)

  // 额度按剩余从少到多，最紧迫的先说。
  const quotaItems = [...lowQuota]
    .sort((left, right) => left.remaining - right.remaining)
    .map<AttentionItem>((credential) => ({
      kind: 'lowQuota',
      groupID: credential.group_id,
      groupName: credential.group_name,
      value: credential.remaining,
      resetAtMS: credential.reset_at_ms,
    }))

  return [...items, ...quotaItems]
}

/** 需要处理的问题总数，用于超限时的汇总文案。 */
export function attentionTotal(items: readonly AttentionItem[]): number {
  return items.reduce((total, item) => total + (item.kind === 'blacklisted' ? item.value : 1), 0)
}
