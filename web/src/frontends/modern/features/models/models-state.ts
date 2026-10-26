import type { LocationQuery } from 'vue-router'
import { positivePage } from '@modern/app/url-state'
import { modelPricingMethods, type ModelFilters, type ModelPricingMethod } from '@modern/api/models'

export const modelStateKeys = [
  'q',
  'group_status',
  'pricing_status',
  'pricing_method',
  'page',
  'page_size',
  'model',
  'source',
] as const
export interface ModelsState extends ModelFilters {
  model: string
  source: number
}
export function parseModelsState(query: LocationQuery): ModelsState {
  return {
    q: typeof query.q === 'string' ? Array.from(query.q.trim()).slice(0, 200).join('') : '',
    groups: query.group_status === 'all' ? 'all' : 'enabled',
    pricing:
      query.pricing_status === 'pending' || query.pricing_status === 'configured'
        ? query.pricing_status
        : 'all',
    method: modelPricingMethods.includes(query.pricing_method as ModelPricingMethod)
      ? (query.pricing_method as ModelPricingMethod)
      : 'all',
    page: positivePage(query.page),
    pageSize: [20, 50, 100].includes(Number(query.page_size)) ? Number(query.page_size) : 20,
    model: typeof query.model === 'string' ? query.model : '',
    source: typeof query.model === 'string' && query.model ? positivePage(query.source, 0) : 0,
  }
}
export function serializeModelsState(state: ModelsState) {
  return {
    ...(state.q ? { q: state.q } : {}),
    ...(state.groups !== 'enabled' ? { group_status: state.groups } : {}),
    ...(state.pricing !== 'all' ? { pricing_status: state.pricing } : {}),
    ...(state.method !== 'all' ? { pricing_method: state.method } : {}),
    ...(state.page > 1 ? { page: String(state.page) } : {}),
    ...(state.pageSize !== 20 ? { page_size: String(state.pageSize) } : {}),
    ...(state.model ? { model: state.model } : {}),
    ...(state.model && state.source ? { source: String(state.source) } : {}),
  }
}
