import {
  applyUsageFilterDraft,
  createUsageFilterDraft,
  parseAppliedUsageFilters,
  resetUsageFilterDraft,
  validateUsageFilterDraft,
} from './usage-filters'

describe('usage filters', () => {
  it('copies applied primitive filters into an independent editable draft', () => {
    const applied = { range: '30d' as const, group_id: 7, model: 'provider/model:Exact' }

    expect(createUsageFilterDraft(applied)).toEqual({
      range: '30d',
      group_id: '7',
      model: 'provider/model:Exact',
    })
    expect(applied).toEqual({ range: '30d', group_id: 7, model: 'provider/model:Exact' })
  })

  it('parses canonical URL state and applies only explicit valid draft filters', () => {
    expect(
      parseAppliedUsageFilters({ tab: 'usage', range: '30d', group_id: '7', model: 'gpt-5' }),
    ).toEqual({ range: '30d', group_id: 7, model: 'gpt-5' })
    expect(
      applyUsageFilterDraft({
        range: '24h',
        group_id: '8',
        model: 'provider/model:Exact',
      }),
    ).toEqual({ range: '24h', group_id: 8, model: 'provider/model:Exact' })
  })

  it('resets the complete draft to the canonical default without applying it', () => {
    expect(resetUsageFilterDraft()).toEqual({ range: '24h', group_id: '', model: '' })
    expect(applyUsageFilterDraft(resetUsageFilterDraft())).toEqual({ range: '24h' })
  })

  it('reports ASCII and Unicode control characters without retaining unsafe values in URL state', () => {
    expect(
      validateUsageFilterDraft({
        range: '30d',
        group_id: '0',
        model: ' leading-model',
      }),
    ).toEqual({
      group_id: 'monitor.usage.errors.positiveId',
      model: 'monitor.usage.errors.model',
    })
    expect(parseAppliedUsageFilters({ range: '24h', model: 'gpt-\u00855.6' })).toEqual({
      range: '24h',
    })
    expect(
      validateUsageFilterDraft({ range: '24h', group_id: '', model: 'gpt-\u00855.6' }),
    ).toEqual({ model: 'monitor.usage.errors.model' })
  })
})
