import { describe, expect, it } from 'vitest'

import { deterministicUUIDPrefix } from './deterministic-ids'

describe('deterministic E2E UUID namespace', () => {
  it('is stable for one attempt and unique across tests and retries', () => {
    const first = deterministicUUIDPrefix({
      parallelIndex: 0,
      repeatEachIndex: 0,
      retry: 0,
      testId: 'business flow creates an AccessKey',
    })

    expect(
      deterministicUUIDPrefix({
        parallelIndex: 0,
        repeatEachIndex: 0,
        retry: 0,
        testId: 'business flow creates an AccessKey',
      }),
    ).toBe(first)
    expect(
      deterministicUUIDPrefix({
        parallelIndex: 0,
        repeatEachIndex: 0,
        retry: 0,
        testId: 'another test creates a Group',
      }),
    ).not.toBe(first)
    expect(
      deterministicUUIDPrefix({
        parallelIndex: 0,
        repeatEachIndex: 0,
        retry: 1,
        testId: 'business flow creates an AccessKey',
      }),
    ).not.toBe(first)
    expect(`${first}000000000001`).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    )
  })
})
