import { CircleSlash, Clock, Coins, KeyRound, TriangleAlert } from '@lucide/vue'
import type { Component } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import type { HealthReport } from '@modern/api/health'

export interface AttentionItem {
  key: string
  icon: Component
  tone: 'danger' | 'warning'
  subject: string
  /** 'home.attention.<detail>' 的后半段，参数由调用处补 */
  detail: string
  params: Record<string, unknown>
  to: RouteLocationRaw
}

const groupRoute = (id: number): RouteLocationRaw => ({
  name: 'modern-group-detail',
  params: { id },
})

/**
 * 首页「需要处理」的取材规则。
 *
 * 读运行健康快照而不是分组列表：分组列表只有凭据计数，推不出额度将尽、
 * 充值卡临期、访问密钥被费用额度挡住这几类。只看计数的话，一个健康实例上
 * 这块永远是空的，看起来就像坏了。
 *
 * 刻意不收冷却：几分钟内自愈，看完不需要做任何事，而且冷却条在运行健康页已有。
 */
export function collectAttention(report: HealthReport | undefined): AttentionItem[] {
  if (!report) return []

  // 分组彻底没有可用凭据：请求会直接失败，最紧急。
  const stalled = report.groups
    .filter(
      (group) => group.enabled && group.counts.credentials > 0 && group.counts.available === 0,
    )
    .map<AttentionItem>((group) => ({
      key: 'stalled-' + group.id,
      icon: CircleSlash,
      tone: 'danger',
      subject: group.name,
      detail: 'stalled',
      params: {},
      to: groupRoute(group.id),
    }))
  const stalledGroups = new Set(stalled.map((item) => item.key.slice('stalled-'.length)))

  // 被拉黑的凭据按分组合并，只报绝对条数：停用的凭据不计入分母，算比例会错。
  const blocked = new Map<number, { name: string; count: number }>()
  for (const credential of report.isolated) {
    const entry = blocked.get(credential.groupID)
    if (entry) entry.count += 1
    else blocked.set(credential.groupID, { name: credential.groupName, count: 1 })
  }

  return [
    ...stalled,
    ...[...blocked.entries()]
      // 已经报过「无可用凭据」的分组不再重复报拉黑数。
      .filter(([id]) => !stalledGroups.has(String(id)))
      .sort(([, left], [, right]) => right.count - left.count)
      .map<AttentionItem>(([id, entry]) => ({
        key: 'blocked-' + id,
        icon: TriangleAlert,
        tone: 'warning',
        subject: entry.name,
        detail: 'blocked',
        params: { count: entry.count },
        to: groupRoute(id),
      })),
    // 额度按剩余从少到多，最紧迫的排前面。
    ...[...report.quotas]
      .sort((left, right) => left.remaining - right.remaining)
      .map<AttentionItem>((quota) => ({
        key: 'quota-' + quota.id,
        icon: Coins,
        tone: 'warning',
        subject: quota.groupName,
        detail: 'quota',
        params: { percent: Math.round(quota.remaining * 100) },
        to: groupRoute(quota.groupID),
      })),
    ...[...report.credits]
      .sort((left, right) => left.expiresAt - right.expiresAt)
      .map<AttentionItem>((credit) => ({
        key: 'credit-' + credit.id,
        icon: Clock,
        tone: 'warning',
        subject: credit.groupName,
        detail: 'credits',
        params: { count: credit.count },
        to: groupRoute(credit.groupID),
      })),
    ...report.accessKeys.map<AttentionItem>((key) => ({
      key: 'key-' + key.id,
      icon: KeyRound,
      tone: 'warning',
      subject: key.name,
      detail: 'keyBlocked',
      params: {},
      to: {
        name: 'modern-access-keys',
        query: { panel: 'detail', access_key: String(key.id) },
      },
    })),
  ]
}
