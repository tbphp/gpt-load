export interface TrendDatum {
  bucket_start: string
  bucket_end: string
  request_count: number
  failure_count: number
}

export interface TrendGeometry {
  requestPath: string
  requestAreaPath: string
  requestPoints: Array<{ x: number; y: number; value: number }>
  requestMarkers: Array<{ x: number; y: number; value: number }>
  failures: Array<{ x: number; height: number; value: number }>
}

interface TrendPoint {
  start: number
  end: number
  x: number
  y: number
  value: number
}

function round(value: number): number {
  return Math.round(value * 100) / 100
}

function assertDimension(value: number): void {
  if (!Number.isFinite(value) || value <= 0) {
    throw new RangeError('Trend chart dimensions must be finite positive numbers')
  }
}

function parseDatum(datum: TrendDatum): { start: number; end: number } {
  const start = Date.parse(datum.bucket_start)
  const end = Date.parse(datum.bucket_end)
  if (
    !Number.isFinite(start) ||
    !Number.isFinite(end) ||
    end <= start ||
    !Number.isSafeInteger(datum.request_count) ||
    datum.request_count < 0 ||
    !Number.isSafeInteger(datum.failure_count) ||
    datum.failure_count < 0 ||
    datum.failure_count > datum.request_count
  ) {
    throw new RangeError('Trend chart data is invalid')
  }
  return { start, end }
}

function segments(points: readonly TrendPoint[]): TrendPoint[][] {
  return points.reduce<TrendPoint[][]>((result, point, index) => {
    const previous = points[index - 1]
    if (previous === undefined || point.start !== previous.end) result.push([point])
    else result.at(-1)?.push(point)
    return result
  }, [])
}

export function buildTrendGeometry(
  series: readonly TrendDatum[],
  width: number,
  requestHeight: number,
  failureHeight: number,
  rangeStart: string,
  rangeEnd: string,
): TrendGeometry {
  assertDimension(width)
  assertDimension(requestHeight)
  assertDimension(failureHeight)
  if (series.length === 0) {
    return {
      requestPath: '',
      requestAreaPath: '',
      requestPoints: [],
      requestMarkers: [],
      failures: [],
    }
  }

  const parsed = series.map(parseDatum)
  const domainStart = Date.parse(rangeStart)
  const domainEnd = Date.parse(rangeEnd)
  const bucketDuration = parsed[0]!.end - parsed[0]!.start
  const pointDomainEnd = domainEnd - bucketDuration
  if (
    !Number.isFinite(domainStart) ||
    !Number.isFinite(domainEnd) ||
    domainEnd <= domainStart ||
    pointDomainEnd < domainStart ||
    parsed.some(
      (datum) =>
        datum.start < domainStart ||
        datum.end > domainEnd ||
        datum.end - datum.start !== bucketDuration,
    )
  ) {
    throw new RangeError('Trend chart range is invalid')
  }
  for (let index = 1; index < parsed.length; index += 1) {
    if (parsed[index]!.start < parsed[index - 1]!.end) {
      throw new RangeError('Trend chart buckets must be ordered and non-overlapping')
    }
  }

  const domainDuration = pointDomainEnd - domainStart
  const maximumRequests = Math.max(0, ...series.map((datum) => datum.request_count))
  const maximumFailures = Math.max(0, ...series.map((datum) => datum.failure_count))
  const requestTopInset = Math.min(14, requestHeight * 0.08)
  const points = series.map<TrendPoint>((datum, index) => {
    const timing = parsed[index]!
    return {
      ...timing,
      x: round(
        domainDuration === 0 ? width / 2 : ((timing.start - domainStart) / domainDuration) * width,
      ),
      y: round(
        maximumRequests === 0
          ? requestHeight
          : requestTopInset +
              (1 - datum.request_count / maximumRequests) * (requestHeight - requestTopInset),
      ),
      value: datum.request_count,
    }
  })

  const requestSegments = segments(points)
  const requestPath = requestSegments
    .map((segment) =>
      segment.map((point, index) => `${index === 0 ? 'M' : 'L'} ${point.x} ${point.y}`).join(' '),
    )
    .join(' ')
  const requestAreaPath = requestSegments
    .map((segment) => {
      const first = segment[0]!
      const last = segment.at(-1)!
      const line = segment.map((point) => `L ${point.x} ${point.y}`).join(' ')
      return `M ${first.x} ${requestHeight} ${line} L ${last.x} ${requestHeight} Z`
    })
    .join(' ')
  const requestMarkers = requestSegments.flatMap((segment, index) =>
    segment.length === 1 || index === requestSegments.length - 1 ? [segment.at(-1)!] : [],
  )
  const failures = series.map((datum, index) => ({
    x: points[index]!.x,
    height: round(
      maximumFailures === 0 ? 0 : (datum.failure_count / maximumFailures) * failureHeight,
    ),
    value: datum.failure_count,
  }))

  return {
    requestPath,
    requestAreaPath,
    requestPoints: points.map(({ x, y, value }) => ({ x, y, value })),
    requestMarkers: requestMarkers.map(({ x, y, value }) => ({ x, y, value })),
    failures,
  }
}
