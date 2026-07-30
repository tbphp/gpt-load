import { normalizeGroupQuery, normalizeGroupTab, parsePositiveId } from './group-route'

describe('Group route parsing', () => {
  it.each([
    ['7', 7],
    ['007', 7],
    [String(Number.MAX_SAFE_INTEGER), Number.MAX_SAFE_INTEGER],
  ])('parses safe positive digit-only ID %s', (raw, expected) => {
    expect(parsePositiveId(raw)).toBe(expected)
  })

  it.each([
    undefined,
    '',
    '0',
    '-1',
    '+7',
    '7.5',
    ' 7',
    '7 ',
    'abc',
    String(Number.MAX_SAFE_INTEGER + 1),
    ['7'],
  ])('rejects unsafe Group identity %j', (raw) => {
    expect(parsePositiveId(raw)).toBeUndefined()
  })

  it.each([
    ['keys', 'keys'],
    ['models', 'models'],
    ['settings', 'settings'],
    [undefined, 'keys'],
    ['unknown', 'keys'],
    [['models'], 'keys'],
  ])('normalizes tab %j to %s', (raw, expected) => {
    expect(normalizeGroupTab(raw)).toBe(expected)
  })

  it.each([
    [
      { tab: 'keys', key_state: 'problem' },
      { tab: 'keys', key_state: 'problem' },
    ],
    [{ tab: 'models', key_state: 'problem' }, { tab: 'models' }],
    [{ tab: 'settings', key_state: 'problem' }, { tab: 'settings' }],
    [{ tab: 'unknown', unsafe: 'discarded' }, { tab: 'keys' }],
    [{ tab: ['keys'], key_state: ['problem'] }, { tab: 'keys' }],
  ])('normalizes Group query %#j to %#j', (query, expected) => {
    expect(normalizeGroupQuery(query)).toEqual(expected)
  })
})
