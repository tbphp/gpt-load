import {
  applyLogFilterDraft,
  createLogFilterDraft,
  parseAppliedLogFilters,
  serializeAppliedLogFilters,
  validateLogFilterDraft,
  type LogFilterDraft,
} from './log-filters'

afterEach(() => vi.unstubAllEnvs())

describe('log filters', () => {
  it('converts optional local datetimes to RFC3339 and back without adding absent bounds', () => {
    vi.stubEnv('TZ', 'UTC')
    const draft: LogFilterDraft = {
      from: '2026-07-25T10:00',
      to: '',
      group_id: '',
      model: '',
      access_key_id: '',
      status: '',
      request_id: '',
    }

    expect(applyLogFilterDraft(draft)).toEqual({
      from: '2026-07-25T10:00:00.000Z',
    })
    expect(createLogFilterDraft({ from: '2026-07-25T10:00:00.000Z' })).toEqual(draft)
  })

  it('parses normalized query values and serializes exact applied filters without pagination', () => {
    const filters = parseAppliedLogFilters({
      tab: 'logs',
      from: '2026-07-25T10:00:00.000Z',
      group_id: '7',
      model: 'provider/model:exact',
      access_key_id: '12',
      status: 'error',
      request_id: 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173',
      selected_request_id: 'b4d4e121-8ac3-4df4-8ceb-63b10ddc6173',
    })

    expect(filters).toEqual({
      from: '2026-07-25T10:00:00.000Z',
      group_id: 7,
      model: 'provider/model:exact',
      access_key_id: 12,
      status: 'error',
      request_id: 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173',
    })
    expect(serializeAppliedLogFilters(filters)).toEqual({
      tab: 'logs',
      from: '2026-07-25T10:00:00.000Z',
      group_id: '7',
      model: 'provider/model:exact',
      access_key_id: '12',
      status: 'error',
      request_id: 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173',
    })
  })

  it.each(['success', 'error', 'incomplete', 'canceled'] as const)(
    'preserves exact model and the %s status while blanks reset every optional filter',
    (status) => {
      const filters = applyLogFilterDraft({
        from: '',
        to: '',
        group_id: '7',
        model: 'provider/model:Exact',
        access_key_id: '12',
        status,
        request_id: 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173',
      })

      expect(filters).toEqual({
        group_id: 7,
        model: 'provider/model:Exact',
        access_key_id: 12,
        status,
        request_id: 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173',
      })
      expect(createLogFilterDraft(filters)).toEqual({
        from: '',
        to: '',
        group_id: '7',
        model: 'provider/model:Exact',
        access_key_id: '12',
        status,
        request_id: 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173',
      })
      expect(applyLogFilterDraft(createLogFilterDraft())).toEqual({})
    },
  )

  it('rejects non-positive IDs, non-canonical UUIDv4, and unknown status using safe locale keys', () => {
    const rawSecret = 'NOT-A-UUID-secret-canary'
    const errors = validateLogFilterDraft({
      from: '',
      to: '',
      group_id: '0',
      model: '',
      access_key_id: '1.5',
      status: 'failed',
      request_id: rawSecret,
    })

    expect(errors).toEqual({
      group_id: 'monitor.logs.errors.positiveId',
      access_key_id: 'monitor.logs.errors.positiveId',
      status: 'monitor.logs.errors.status',
      request_id: 'monitor.logs.errors.requestId',
    })
    expect(JSON.stringify(errors)).not.toContain(rawSecret)
    expect(
      validateLogFilterDraft({
        ...createLogFilterDraft(),
        request_id: 'A4D4E121-8AC3-4DF4-8CEB-63B10DDC6173',
      }),
    ).toEqual({ request_id: 'monitor.logs.errors.requestId' })
  })

  it('requires valid local datetimes and a strictly increasing optional time range', () => {
    const base = createLogFilterDraft()

    expect(
      validateLogFilterDraft({
        ...base,
        from: '2026-02-30T10:00',
      }),
    ).toEqual({ from: 'monitor.logs.errors.dateTime' })
    expect(
      validateLogFilterDraft({
        ...base,
        from: '2026-07-25T10:00',
        to: '2026-07-25T10:00',
      }),
    ).toEqual({ to: 'monitor.logs.errors.range' })
    expect(
      validateLogFilterDraft({
        ...base,
        from: '2026-07-25T10:00',
        to: '2026-07-25T10:01',
      }),
    ).toEqual({})
  })

  it.each([' leading-model', 'trailing-model ', 'model\u0000canary', 'model\u007fcanary'])(
    'rejects an exact model containing surrounding whitespace or controls: %j',
    (model) => {
      const errors = validateLogFilterDraft({
        ...createLogFilterDraft(),
        model,
      })

      expect(errors).toEqual({ model: 'monitor.logs.errors.model' })
      expect(JSON.stringify(errors)).not.toContain(model)
    },
  )
})
