import type {
  ModelPriceDto,
  ModelPriceSlotsDto,
  ModelPriceMethod,
} from '@/app/resources/model-prices'

import { modelPriceFields, type ModelPriceField } from './model-price-form'

export type ModelPriceValueState = 'unavailable' | 'free' | 'configured'

export interface ModelPriceSlotPresentation {
  field: ModelPriceField
  value: string
  state: ModelPriceValueState
}

export interface ModelPricePresentation {
  row: ModelPriceDto
  slots: readonly ModelPriceSlotPresentation[]
  method: ModelPriceMethod | null
}

export interface ModelPricePresenterOptions {
  unavailable: string
  free: string
  configured(value: string): string
}

export function presentModelPrice(
  row: ModelPriceDto,
  options: ModelPricePresenterOptions,
): ModelPricePresentation {
  const slots = modelPriceFields.map((field): ModelPriceSlotPresentation => {
    const value = row.prices[field as keyof ModelPriceSlotsDto]
    if (value === null) return { field, value: options.unavailable, state: 'unavailable' }
    if (value === '0') return { field, value: options.free, state: 'free' }
    return { field, value: options.configured(value), state: 'configured' }
  })
  return { row, slots, method: row.method }
}
