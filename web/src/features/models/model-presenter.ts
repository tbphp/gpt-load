import type { ModelPriceBranchDto, ModelRouteGroupDto } from '@/app/resources/models'
import type { ClientModelDto, ModelUpstreamDto } from '@/app/resources/models'
import type { ModelPriceSlotsDto } from '@/app/resources/model-prices'

/** 列表中直接展示的两个价格槽位，其余槽位只在展开详情中出现。 */
export type ModelSummaryField = Extract<keyof ModelPriceSlotsDto, 'input' | 'output'>

export type ModelPriceSummaryState = 'unavailable' | 'free' | 'single' | 'range'

export interface ModelPriceFieldSummary {
  field: ModelSummaryField
  state: ModelPriceSummaryState
  /** 原始 decimal 字符串，保持后端精度；range 时为区间下界。 */
  value: string | null
  /** range 时的区间上界。 */
  upper: string | null
}

export type ModelUpstreamShape = 'direct' | 'alias' | 'multi'

export interface ModelUpstreamSummary {
  shape: ModelUpstreamShape
  /** shape 为 alias 时的上游模型 ID。 */
  modelID: string | null
  count: number
}

export interface ModelScopePresentation {
  upstream: ModelUpstreamDto
  branch: ModelPriceBranchDto
}

export interface ClientModelPresentation {
  model: ClientModelDto
  scopes: ModelScopePresentation[]
  pendingCount: number
  status: 'configured' | 'pending'
  upstream: ModelUpstreamSummary
  routeGroups: ModelRouteGroupDto[]
  input: ModelPriceFieldSummary
  output: ModelPriceFieldSummary
  /** 仅有一套价格时可以从列表直接编辑。 */
  soleBranch: ModelPriceBranchDto | null
}

function summarizeUpstream(model: ClientModelDto): ModelUpstreamSummary {
  const count = model.upstream_models.length
  if (count !== 1) return { shape: 'multi', modelID: null, count }
  const upstream = model.upstream_models[0]
  if (upstream === undefined) return { shape: 'multi', modelID: null, count }
  return upstream.alias_applied
    ? { shape: 'alias', modelID: upstream.model_id, count }
    : { shape: 'direct', modelID: upstream.model_id, count }
}

function collectRouteGroups(scopes: ModelScopePresentation[]): ModelRouteGroupDto[] {
  const groups = new Map<number, ModelRouteGroupDto>()
  for (const { branch } of scopes) {
    for (const group of branch.route_groups) {
      if (!groups.has(group.id)) groups.set(group.id, group)
    }
  }
  return [...groups.values()].sort((left, right) =>
    left.name === right.name ? left.id - right.id : left.name.localeCompare(right.name),
  )
}

function summarizeField(
  scopes: ModelScopePresentation[],
  field: ModelSummaryField,
): ModelPriceFieldSummary {
  const priced = scopes
    .map(({ branch }) => branch.price.prices[field])
    .filter((value): value is string => value !== null)
  if (priced.length === 0) {
    return { field, state: 'unavailable', value: null, upper: null }
  }

  let lower = priced[0] as string
  let upper = priced[0] as string
  for (const value of priced) {
    if (Number(value) < Number(lower)) lower = value
    if (Number(value) > Number(upper)) upper = value
  }
  if (Number(lower) !== Number(upper)) {
    return { field, state: 'range', value: lower, upper }
  }
  return { field, state: Number(lower) === 0 ? 'free' : 'single', value: lower, upper: null }
}

export function presentClientModel(model: ClientModelDto): ClientModelPresentation {
  const scopes = model.upstream_models.flatMap((upstream) =>
    upstream.prices.map((branch) => ({ upstream, branch })),
  )
  const pendingCount = scopes.filter(
    ({ branch }) => branch.price.pricing_status === 'pending',
  ).length
  return {
    model,
    scopes,
    pendingCount,
    status: pendingCount > 0 ? 'pending' : 'configured',
    upstream: summarizeUpstream(model),
    routeGroups: collectRouteGroups(scopes),
    input: summarizeField(scopes, 'input'),
    output: summarizeField(scopes, 'output'),
    soleBranch: scopes.length === 1 ? ((scopes[0] as ModelScopePresentation).branch ?? null) : null,
  }
}

/** 价格范围是否影响了当前路由分组之外的分组。 */
export function hasWiderPriceImpact(branch: ModelPriceBranchDto): boolean {
  if (branch.route_groups.length !== branch.affected_groups.length) return true
  const routed = new Set(branch.route_groups.map(({ id }) => id))
  return branch.affected_groups.some(({ id }) => !routed.has(id))
}
