import type { ClientModelDto, ModelUpstreamDto } from '@/app/resources/models'

import { modelPriceFields, type ModelPriceField } from '../model-prices/model-price-form'

export type ModelPriceRowStatus = 'configured' | 'pending' | 'unpriced'

export interface ModelUpstreamRow {
  upstream: ModelUpstreamDto
  status: ModelPriceRowStatus
  /** 基础价格槽位；null 表示未设置，由视图渲染占位符。 */
  prices: Record<ModelPriceField, string | null>
  fastPrices: Record<ModelPriceField, string | null> | null
  tierCount: number
}

export interface ClientModelRow {
  model: ClientModelDto
  upstreams: ModelUpstreamRow[]
  status: ModelPriceRowStatus
}

function upstreamStatus(upstream: ModelUpstreamDto): ModelPriceRowStatus {
  if (upstream.price.method === 'user_marked_unpriced') return 'unpriced'
  return upstream.price.pricing_status === 'pending' ? 'pending' : 'configured'
}

function presentUpstream(upstream: ModelUpstreamDto): ModelUpstreamRow {
  const prices = {} as Record<ModelPriceField, string | null>
  for (const field of modelPriceFields) prices[field] = upstream.price.prices[field]
  return {
    upstream,
    status: upstreamStatus(upstream),
    prices,
    fastPrices: upstream.price.mode_prices.fast ?? null,
    tierCount: upstream.price.context_tiers.length,
  }
}

export function presentClientModel(model: ClientModelDto): ClientModelRow {
  const upstreams = model.upstream_models.map(presentUpstream)
  const pendingCount = upstreams.filter((row) => row.status === 'pending').length
  const hasUnpriced = upstreams.some((row) => row.status === 'unpriced')
  return {
    model,
    upstreams,
    status: pendingCount > 0 ? 'pending' : hasUnpriced ? 'unpriced' : 'configured',
  }
}
