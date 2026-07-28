import type {
  ModelPricePolicyDto,
  ModelPriceRuleDto,
  ModelPriceSource,
  ModelPriceValues,
} from '@/api/control/model-prices'

export type ModelPriceField = keyof ModelPriceValues
export type ModelPriceValueState = 'not-configured' | 'free' | 'configured'

export interface ModelPriceRowPresentation {
  field: ModelPriceField
  label: string
  value: string
  state: ModelPriceValueState
}

export interface ModelPricePresentation {
  pattern: string
  source: string
  kind: string
  priceRows: readonly ModelPriceRowPresentation[]
  updatedAt: string
  sourceUrl?: string
  policySummary?: string
  globalOverride: boolean
}

export interface ModelPricePresenterOptions {
  fieldLabels: Record<ModelPriceField, string>
  notConfigured: string
  explicitlyFree: string
  configuredPrice(value: number): string
  kindLabel(pattern: string): string
  sourceLabel(source: ModelPriceSource): string
  policySummary(policy: ModelPricePolicyDto): string
}

const priceFields: readonly ModelPriceField[] = [
  'uncached_input',
  'cache_read',
  'cache_write_5m',
  'cache_write_1h',
  'output',
]

export function presentModelPriceRule(
  rule: ModelPriceRuleDto,
  options: ModelPricePresenterOptions,
): ModelPricePresentation {
  const priceRows = priceFields.map((field): ModelPriceRowPresentation => {
    const price = rule.prices[field]
    if (price === null) {
      return {
        field,
        label: options.fieldLabels[field],
        value: options.notConfigured,
        state: 'not-configured',
      }
    }
    if (price === 0) {
      return {
        field,
        label: options.fieldLabels[field],
        value: options.explicitlyFree,
        state: 'free',
      }
    }
    return {
      field,
      label: options.fieldLabels[field],
      value: options.configuredPrice(price),
      state: 'configured',
    }
  })

  return {
    pattern: rule.pattern,
    source: options.sourceLabel(rule.source),
    kind: options.kindLabel(rule.pattern),
    priceRows,
    updatedAt: rule.updated_at,
    ...(rule.source_url ? { sourceUrl: rule.source_url } : {}),
    ...(rule.pricing_policy ? { policySummary: options.policySummary(rule.pricing_policy) } : {}),
    globalOverride: rule.source === 'user' && rule.pattern === '*',
  }
}
