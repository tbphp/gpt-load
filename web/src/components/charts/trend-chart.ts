export interface TrendDatum {
  bucket_start_ms: number
  bucket_end_ms: number
  request_count: number
  failure_count: number
}

export interface TrendPoint {
  x: number
  y: number
}

export interface TrendFailureBar {
  x: number
  y: number
  width: number
  height: number
  value: number
}

export interface TrendGeometry {
  requestPath: string
  requestAreaPath: string
  requestPoints: TrendPoint[]
  failureBars: TrendFailureBar[]
}

interface ParsedDatum {
  start: number
  end: number
  requestCount: number
  failureCount: number
}

function round(value: number): number {
  return Math.round(value * 100) / 100
}

function validDimension(value: number): boolean {
  return Number.isFinite(value) && value > 0
}

function parseDatum(datum: TrendDatum): ParsedDatum | undefined {
  const {
    bucket_start_ms: start,
    bucket_end_ms: end,
    request_count: requestCount,
    failure_count: failureCount,
  } = datum
  if (
    !Number.isSafeInteger(start) ||
    start < 0 ||
    !Number.isSafeInteger(end) ||
    end <= start ||
    !Number.isSafeInteger(requestCount) ||
    requestCount < 0 ||
    !Number.isSafeInteger(failureCount) ||
    failureCount < 0 ||
    failureCount > requestCount
  ) {
    return undefined
  }
  return { start, end, requestCount, failureCount }
}

export function isTrendSeriesUsable(
  series: readonly TrendDatum[],
  rangeStart: number,
  rangeEnd: number,
): boolean {
  if (
    !Number.isSafeInteger(rangeStart) ||
    rangeStart < 0 ||
    !Number.isSafeInteger(rangeEnd) ||
    rangeEnd <= rangeStart
  ) {
    return false
  }

  let previousEnd = rangeStart
  for (const datum of series) {
    const parsed = parseDatum(datum)
    if (
      !parsed ||
      parsed.start < rangeStart ||
      parsed.end > rangeEnd ||
      parsed.start < previousEnd
    ) {
      return false
    }
    previousEnd = parsed.end
  }
  return true
}

function appendSegmentPath(points: readonly TrendPoint[]): string {
  return points.map((point, index) => `${index === 0 ? 'M' : 'L'} ${point.x} ${point.y}`).join(' ')
}

function appendAreaPath(points: readonly TrendPoint[], requestHeight: number): string {
  const first = points[0]
  const last = points.at(-1)
  if (!first || !last) return ''
  return `M ${first.x} ${requestHeight} ${appendSegmentPath(points).replace(/^M /u, 'L ')} L ${last.x} ${requestHeight} Z`
}

export function buildTrendGeometry(
  series: readonly TrendDatum[],
  width: number,
  requestHeight: number,
  failureHeight: number,
  rangeStart: number,
  rangeEnd: number,
): TrendGeometry {
  const empty: TrendGeometry = {
    requestPath: '',
    requestAreaPath: '',
    requestPoints: [],
    failureBars: [],
  }
  if (
    !validDimension(width) ||
    !validDimension(requestHeight) ||
    !validDimension(failureHeight) ||
    series.length === 0 ||
    !isTrendSeriesUsable(series, rangeStart, rangeEnd)
  ) {
    return empty
  }

  const parsed = series.map(parseDatum) as ParsedDatum[]
  const rangeDuration = rangeEnd - rangeStart
  const maximumRequests = Math.max(...parsed.map((datum) => datum.requestCount))
  const maximumFailures = Math.max(...parsed.map((datum) => datum.failureCount))
  const requestTopInset = Math.min(14, requestHeight * 0.08)
  const points = parsed.map<TrendPoint>((datum) => {
    const midpoint = datum.start + (datum.end - datum.start) / 2
    return {
      x: round(((midpoint - rangeStart) / rangeDuration) * width),
      y: round(
        maximumRequests === 0
          ? requestHeight
          : requestTopInset +
              (1 - datum.requestCount / maximumRequests) * (requestHeight - requestTopInset),
      ),
    }
  })

  const segments: TrendPoint[][] = []
  for (let index = 0; index < points.length; index += 1) {
    const point = points[index]!
    const previous = parsed[index - 1]
    const current = parsed[index]!
    if (!previous || current.start !== previous.end) segments.push([point])
    else segments.at(-1)?.push(point)
  }

  return {
    requestPath: segments.map(appendSegmentPath).join(' '),
    requestAreaPath: segments.map((segment) => appendAreaPath(segment, requestHeight)).join(' '),
    requestPoints: points,
    failureBars: parsed.map((datum, index) => {
      const height =
        maximumFailures === 0 ? 0 : round((datum.failureCount / maximumFailures) * failureHeight)
      const rawWidth = ((datum.end - datum.start) / rangeDuration) * width
      const barWidth = Math.max(2, Math.min(rawWidth * 0.68, width))
      const point = points[index]!
      return {
        x: round(Math.max(0, Math.min(width - barWidth, point.x - barWidth / 2))),
        y: round(failureHeight - height),
        width: round(barWidth),
        height,
        value: datum.failureCount,
      }
    }),
  }
}
