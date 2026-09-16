import { CircleSlash, TriangleAlert } from '@lucide/vue'
import type { Component } from 'vue'
import type { GroupRow } from '@modern/api/groups'

export interface AttentionItem {
  key: string
  id: number
  icon: Component
  tone: 'danger' | 'warning'
  subject: string
  /** 文案键，调用处补 count 等参数 */
  detail: 'stalled' | 'blocked'
  count: number
}

/**
 * 首页「需要处理」的取材规则。
 *
 * 只收「不处理就一直坏着」的两类：分组彻底没有可用凭据、以及个别凭据被拉黑。
 * 刻意不收冷却：几分钟内自愈，看了也不用做任何事。
 * 也刻意不收额度：订阅账号那块已经按账号画了额度条，这里再说一遍就是同一件事两个说法。
 *
 * 状态条上的计数与右栏面板读同一份结果，避免两处各算一遍后对不上。
 */
export function collectAttention(groups: readonly GroupRow[]): AttentionItem[] {
  const stalled: AttentionItem[] = []
  const degraded: AttentionItem[] = []
  for (const group of groups) {
    if (!group.enabled || group.credentials.total === 0) continue
    if (group.credentials.available === 0)
      stalled.push({
        key: 'stalled-' + group.id,
        id: group.id,
        icon: CircleSlash,
        tone: 'danger',
        subject: group.name,
        detail: 'stalled',
        count: 0,
      })
    else if (group.credentials.blacklisted > 0)
      degraded.push({
        key: 'blocked-' + group.id,
        id: group.id,
        icon: TriangleAlert,
        tone: 'warning',
        subject: group.name,
        detail: 'blocked',
        count: group.credentials.blacklisted,
      })
  }
  return [...stalled, ...degraded]
}
