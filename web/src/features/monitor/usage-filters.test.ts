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
      group_id: '7',
      model: 'provider/model:Exact',
    })
    expect(applied).toEqual({ range: '30d', group_id: 7, model: 'provider/model:Exact' })
  })

  it('parses canonical URL state and applies only explicit valid draft filters', () => {
    expect(
      parseAppliedUsageFilters({ tab: 'usage', range: '30d', group_id: '7', model: 'gpt-5' }),
    ).toEqual({ range: '30d', group_id: 7, model: 'gpt-5' })
    expect(applyUsageFilterDraft('24h', { group_id: '8', model: 'provider/model:Exact' })).toEqual({
      range: '24h',
      group_id: 8,
      model: 'provider/model:Exact',
    })
  })

  it('resets draft-only filters without changing the selected range', () => {
    expect(resetUsageFilterDraft()).toEqual({ group_id: '', model: '' })
    expect(applyUsageFilterDraft('30d', resetUsageFilterDraft())).toEqual({ range: '30d' })
  })

  it('reports ASCII and Unicode control characters without retaining unsafe values in URL state', () => {
    expect(validateUsageFilterDraft({ group_id: '0', model: ' leading-model' })).toEqual({
      group_id: 'monitor.usage.errors.positiveId',
      model: 'monitor.usage.errors.model',
    })
    expect(parseAppliedUsageFilters({ range: '24h', model: 'gpt-\u00855.6' })).toEqual({
      range: '24h',
    })
    expect(validateUsageFilterDraft({ group_id: '', model: 'gpt-\u00855.6' })).toEqual({
      model: 'monitor.usage.errors.model',
    })
  })
})
