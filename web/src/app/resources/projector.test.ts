import { describe, expect, it } from 'vitest'

import { InvalidResponseError } from '@/api/errors'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectFiniteNumber,
  projectHTTPURL,
  projectISOInstant,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

describe('resource projector primitives', () => {
  it('projects records and arrays without trusting malformed containers', () => {
    expect(projectRecord({ value: true })).toEqual({ value: true })
    expect(projectArray(['a', 'b'], projectString)).toEqual(['a', 'b'])

    for (const value of [null, [], 'record']) {
      expect(() => projectRecord(value)).toThrow(InvalidResponseError)
    }
    expect(() => projectArray(['valid', 2], projectString)).toThrow(InvalidResponseError)
  })

  it('accepts only safe integers and finite numbers within explicit bounds', () => {
    expect(projectSafeInteger(9, { minimum: 1 })).toBe(9)
    expect(projectFiniteNumber(1.25, { minimum: 0 })).toBe(1.25)

    for (const value of [0, 1.5, Number.MAX_SAFE_INTEGER + 1, Number.NaN]) {
      expect(() => projectSafeInteger(value, { minimum: 1 })).toThrow(InvalidResponseError)
    }
    for (const value of [-1, Number.POSITIVE_INFINITY, Number.NaN, '1']) {
      expect(() => projectFiniteNumber(value, { minimum: 0 })).toThrow(InvalidResponseError)
    }
  })

  it('projects booleans, enums, ISO instants, and HTTP(S)-only URLs', () => {
    expect(projectBoolean(true)).toBe(true)
    expect(projectEnum('active', ['active', 'disabled'] as const)).toBe('active')
    expect(projectISOInstant('2026-07-29T01:02:03Z')).toBe('2026-07-29T01:02:03Z')
    expect(projectHTTPURL('https://api.example.com/v1')).toBe('https://api.example.com/v1')
    expect(projectHTTPURL('http://127.0.0.1:8080')).toBe('http://127.0.0.1:8080')

    expect(() => projectBoolean(1)).toThrow(InvalidResponseError)
    expect(() => projectEnum('paused', ['active', 'disabled'] as const)).toThrow(
      InvalidResponseError,
    )
    expect(() => projectISOInstant('next Tuesday')).toThrow(InvalidResponseError)
    expect(() => projectHTTPURL('javascript:alert(1)')).toThrow(InvalidResponseError)
    expect(() => projectHTTPURL('ftp://example.com/file')).toThrow(InvalidResponseError)
  })

  it('fails closed when an unknown additive field looks secret-like', () => {
    const record = projectRecord({
      id: 1,
      masked_key: 'allowed metadata',
      future_value: true,
    })
    expect(() => assertNoSecretLikeFields(record, ['id', 'masked_key'])).not.toThrow()
    expect(() =>
      assertNoSecretLikeFields({ ...record, future_secret_token: 'plaintext' }, [
        'id',
        'masked_key',
      ]),
    ).toThrow(InvalidResponseError)
  })
})
