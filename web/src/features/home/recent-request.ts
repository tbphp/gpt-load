import type { RequestLogItemDto } from '@/app/resources/request-logs'
import type { StatusTone } from '@/components/ui/status-presenter'

export interface RecentRequestOutcome {
  tone: StatusTone
  /** i18n key，相对 `home.ledger.recentRequest` */
  labelKey: string
  /** 有意义时展示的状态码；成功、取消或请求未发出时为 null */
  statusCode: number | null
}

/** 深链窗口的前后留白，保证目标请求落在日志列表的默认时间窗内。 */
const logWindowPaddingMS = 60 * 60 * 1000

/**
 * 把一条请求日志归成首页回执要展示的结论。
 *
 * 状态码只从这条日志的 status_code 字段读出来展示，绝不用它反过来做查询条件：
 * status_code 上没有索引，拿它当过滤条件会让「取最近一条」退化成全表扫描 + 排序。
 */
export function recentRequestOutcome(log: RequestLogItemDto): RecentRequestOutcome {
  const statusCode = log.status_code > 0 ? log.status_code : null
  if (log.status === 'success') return { tone: 'success', labelKey: 'success', statusCode: null }
  if (log.status === 'canceled') return { tone: 'neutral', labelKey: 'canceled', statusCode: null }
  if (log.status === 'incomplete') return { tone: 'warning', labelKey: 'incomplete', statusCode }
  return { tone: 'danger', labelKey: failureReasonKey(log.status_code), statusCode }
}

function failureReasonKey(statusCode: number): string {
  if (statusCode === 0) return 'reasons.notSent'
  if (statusCode === 429) return 'reasons.rateLimited'
  if (statusCode === 401 || statusCode === 403) return 'reasons.unauthorized'
  if (statusCode === 404) return 'reasons.notFound'
  if (statusCode === 408 || statusCode === 504) return 'reasons.timeout'
  if (statusCode >= 500) return 'reasons.upstream'
  if (statusCode >= 400) return 'reasons.badRequest'
  return 'reasons.unknown'
}

/**
 * 日志深链要带的时间窗口。
 *
 * 不带的话监控页会套用默认的「近 24 小时」，而最近一次请求完全可能早于 24 小时
 * （个人自部署周末不用很常见）——那时详情抽屉能打开，关掉却是一个空列表。
 */
export function recentRequestLogWindow(completedAtMS: number): {
  from_ms: string
  to_ms: string
} {
  return {
    from_ms: String(Math.max(0, completedAtMS - logWindowPaddingMS)),
    to_ms: String(completedAtMS + logWindowPaddingMS),
  }
}
