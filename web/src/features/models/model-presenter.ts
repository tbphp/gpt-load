import type { ClientModelDto, ModelPriceBranchDto, ModelUpstreamDto } from '@/app/resources/models'

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
  }
}

/** 价格范围是否影响了当前路由分组之外的分组。 */
export function hasWiderPriceImpact(branch: ModelPriceBranchDto): boolean {
  if (branch.route_groups.length !== branch.affected_groups.length) return true
  const routed = new Set(branch.route_groups.map(({ id }) => id))
  return branch.affected_groups.some(({ id }) => !routed.has(id))
}
