import { normalizeMonitorQuery, normalizeMonitorTab, sameMonitorQuery } from './monitor-route'

describe('monitor route normalization', () => {
  it('defaults an absent, legacy, or multi-valued tab to Health without retaining unrelated query values', () => {
    expect(normalizeMonitorQuery({})).toEqual({ tab: 'health' })
    expect(normalizeMonitorQuery({ tab: 'requests' })).toEqual({ tab: 'health' })
    expect(normalizeMonitorQuery({ tab: ['logs', 'health'], key: 'secret' })).toEqual({
      tab: 'health',
    })
  })

  it('retains only valid Inspector inputs and drops keys that belong to another tab', () => {
    expect(
      normalizeMonitorQuery({
        tab: 'inspector',
        protocol: 'openai',
        external_model: 'gpt-real',
        access_key_id: '12',
        cursor: 'drop-me',
      }),
    ).toEqual({
      tab: 'inspector',
      protocol: 'openai',
      external_model: 'gpt-real',
      access_key_id: '12',
    })
  })

  it('rejects unsafe scalar, identity, enum, timestamp, and request-id filters', () => {
    expect(
      normalizeMonitorQuery({
        tab: 'logs',
        from: '2026-07-25T11:00:00.000Z',
        to: '2026-07-25T10:00:00.000Z',
        group_id: '0',
        model: ['gpt-real'],
        access_key_id: '12.5',
        status: 'failed',
        request_id: 'A4D4E121-8AC3-4DF4-8CEB-63B10DDC6173',
        cursor: ['opaque'],
      }),
    ).toEqual({ tab: 'logs' })
  })

  it('retains a valid Logs filter set in canonical field order', () => {
    expect(
      normalizeMonitorQuery({
        status: 'error',
        tab: 'logs',
        to: '2026-07-25T11:00:00.000Z',
        model: 'gpt-real',
        group_id: '7',
        request_id: 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173',
        from: '2026-07-25T10:00:00.000Z',
        access_key_id: '12',
      }),
    ).toEqual({
      tab: 'logs',
      from: '2026-07-25T10:00:00.000Z',
      to: '2026-07-25T11:00:00.000Z',
      group_id: '7',
      model: 'gpt-real',
      access_key_id: '12',
      status: 'error',
      request_id: 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173',
    })
  })

  it('drops pagination cursors so they cannot enter Logs URL history', () => {
    expect(normalizeMonitorQuery({ tab: 'logs', cursor: 'opaque' })).toEqual({ tab: 'logs' })
  })

  it('drops only an inverted time range while keeping independent valid Logs filters', () => {
    expect(
      normalizeMonitorQuery({
        tab: 'logs',
        from: '2026-07-25T11:00:00.000Z',
        to: '2026-07-25T10:00:00.000Z',
        status: 'error',
      }),
    ).toEqual({ tab: 'logs', status: 'error' })
  })

  it('drops an equal time range while keeping independent valid Logs filters', () => {
    expect(
      normalizeMonitorQuery({
        tab: 'logs',
        from: '2026-07-25T10:00:00.000Z',
        to: '2026-07-25T10:00:00.000Z',
        status: 'error',
      }),
    ).toEqual({ tab: 'logs', status: 'error' })
  })

  it.each([
    ['health', 'health'],
    ['logs', 'logs'],
    ['inspector', 'inspector'],
    [undefined, 'health'],
    [['logs'], 'health'],
  ])('normalizes tab %j to %s', (raw, expected) => {
    expect(normalizeMonitorTab(raw)).toBe(expected)
  })

  it('compares query maps by their allowed key-value pairs instead of object identity', () => {
    expect(
      sameMonitorQuery({ tab: 'logs', status: 'error' }, { status: 'error', tab: 'logs' }),
    ).toBe(true)
    expect(
      sameMonitorQuery({ tab: 'logs', status: 'error' }, { tab: 'logs', status: 'success' }),
    ).toBe(false)
  })
})
