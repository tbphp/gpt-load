import { buildTrendGeometry, type TrendDatum } from './trend-chart'

function datum(
  bucketStart: string,
  requestCount: number,
  failureCount: number,
  durationHours = 1,
): TrendDatum {
  const start = new Date(bucketStart)
  return {
    bucket_start: bucketStart,
    bucket_end: new Date(start.getTime() + durationHours * 60 * 60 * 1000).toISOString(),
    request_count: requestCount,
    failure_count: failureCount,
  }
}

describe('buildTrendGeometry', () => {
  it('returns finite baseline geometry when every value is zero', () => {
    const geometry = buildTrendGeometry(
      [datum('2026-07-29T00:00:00.000Z', 0, 0), datum('2026-07-29T01:00:00.000Z', 0, 0)],
      100,
      60,
      20,
    )

    expect(geometry.requestPath).toBe('M 0 60 L 100 60')
    expect(geometry.requestAreaPath).toBe('M 0 60 L 0 60 L 100 60 L 100 60 Z')
    expect(geometry.requests).toEqual([
      { x: 0, y: 60, value: 0 },
      { x: 100, y: 60, value: 0 },
    ])
    expect(geometry.failures).toEqual([
      { x: 0, height: 0, value: 0 },
      { x: 100, height: 0, value: 0 },
    ])
    expect(JSON.stringify(geometry)).not.toMatch(/NaN|Infinity/)
  })

  it('keeps a single point visible without inventing another bucket', () => {
    const geometry = buildTrendGeometry([datum('2026-07-29T00:00:00.000Z', 8, 2)], 120, 80, 24)

    expect(geometry.requestPath).toBe('M 60 0')
    expect(geometry.requests).toEqual([{ x: 60, y: 0, value: 8 }])
    expect(geometry.failures).toEqual([{ x: 60, height: 24, value: 2 }])
  })

  it('starts a new request subpath for sparse buckets instead of filling the gap', () => {
    const geometry = buildTrendGeometry(
      [datum('2026-07-29T00:00:00.000Z', 2, 0), datum('2026-07-29T02:00:00.000Z', 4, 1)],
      120,
      60,
      18,
    )

    expect(geometry.requestPath.match(/\bM\b/g)).toHaveLength(2)
    expect(geometry.failures).toHaveLength(2)
  })

  it('keeps equal request values stable and scales the failure peak independently', () => {
    const geometry = buildTrendGeometry(
      [
        datum('2026-07-29T00:00:00.000Z', 5, 1),
        datum('2026-07-29T01:00:00.000Z', 5, 4),
        datum('2026-07-29T02:00:00.000Z', 5, 2),
      ],
      200,
      80,
      32,
    )

    expect(geometry.requestPath).toBe('M 0 0 L 100 0 L 200 0')
    expect(geometry.failures.map(({ height }) => height)).toEqual([8, 32, 16])
    expect(JSON.stringify(geometry)).not.toMatch(/NaN|Infinity/)
  })

  it.each([
    [0, 80, 20],
    [Number.NaN, 80, 20],
    [100, 0, 20],
    [100, 80, Number.POSITIVE_INFINITY],
  ])('rejects invalid chart dimensions (%s, %s, %s)', (width, requestHeight, failureHeight) => {
    expect(() => buildTrendGeometry([], width, requestHeight, failureHeight)).toThrow(RangeError)
  })
})
