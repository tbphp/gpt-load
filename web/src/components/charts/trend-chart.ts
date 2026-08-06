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
  plotTop: number
  baseline: number
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

export function completeTrendSeries(
  series: readonly TrendDatum[],
  rangeStart: number,
  rangeEnd: number,
): TrendDatum[] {
  if (series.length === 0 || !isTrendSeriesUsable(series, rangeStart, rangeEnd)) {
    return [...series]
  }

  const bucketDuration = series[0]!.bucket_end_ms - series[0]!.bucket_start_ms
  const rangeDuration = rangeEnd - rangeStart
  if (
    rangeDuration % bucketDuration !== 0 ||
    series.some(
      (datum) =>
        datum.bucket_end_ms - datum.bucket_start_ms !== bucketDuration ||
        (datum.bucket_start_ms - rangeStart) % bucketDuration !== 0,
    )
  ) {
    return [...series]
  }

  const byStart = new Map(series.map((datum) => [datum.bucket_start_ms, datum]))
  const result: TrendDatum[] = []
  for (let bucketStart = rangeStart; bucketStart < rangeEnd; bucketStart += bucketDuration) {
    result.push(
      byStart.get(bucketStart) ?? {
        bucket_start_ms: bucketStart,
        bucket_end_ms: bucketStart + bucketDuration,
        request_count: 0,
        failure_count: 0,
      },
    )
  }
  return result
}

function appendSegmentPath(points: readonly TrendPoint[]): string {
  return points.map((point, index) => `${index === 0 ? 'M' : 'L'} ${point.x} ${point.y}`).join(' ')
}

function appendAreaPath(points: readonly TrendPoint[], baseline: number): string {
  const first = points[0]
  const last = points.at(-1)
  if (!first || !last) return ''
  return `M ${first.x} ${baseline} ${appendSegmentPath(points).replace(/^M /u, 'L ')} L ${last.x} ${baseline} Z`
}

export function buildTrendGeometry(
  series: readonly TrendDatum[],
  width: number,
  chartHeight: number,
  maximumFailureHeight: number,
  rangeStart: number,
  rangeEnd: number,
): TrendGeometry {
  const plotTop = validDimension(chartHeight) ? Math.min(14, chartHeight * 0.1) : 0
  const plotBottomInset = validDimension(chartHeight) ? Math.min(8, chartHeight * 0.1) : 0
  const baseline = validDimension(chartHeight) ? chartHeight - plotBottomInset : 0
  const empty: TrendGeometry = {
    requestPath: '',
    requestAreaPath: '',
    requestPoints: [],
    failureBars: [],
    plotTop,
    baseline,
  }
  if (
    !validDimension(width) ||
    !validDimension(chartHeight) ||
    !validDimension(maximumFailureHeight) ||
    series.length === 0 ||
    !isTrendSeriesUsable(series, rangeStart, rangeEnd)
  ) {
    return empty
  }

  const parsed = series.map(parseDatum) as ParsedDatum[]
  const rangeDuration = rangeEnd - rangeStart
  const maximumRequests = Math.max(...parsed.map((datum) => datum.requestCount)) * 1.08
  const maximumFailures = Math.max(...parsed.map((datum) => datum.failureCount))
  const failureHeight = Math.min(maximumFailureHeight, baseline - plotTop)
  const bucketDuration = parsed[0]!.end - parsed[0]!.start
  const pointRangeDuration = rangeDuration - bucketDuration
  const points = parsed.map<TrendPoint>((datum) => {
    return {
      x: round(
        pointRangeDuration === 0
          ? width / 2
          : ((datum.start - rangeStart) / pointRangeDuration) * width,
      ),
      y: round(
        maximumRequests === 0
          ? baseline
          : plotTop + (1 - datum.requestCount / maximumRequests) * (baseline - plotTop),
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
    requestAreaPath: segments.map((segment) => appendAreaPath(segment, baseline)).join(' '),
    requestPoints: points,
    failureBars: parsed.map((datum, index) => {
      const height =
        maximumFailures === 0 || datum.failureCount === 0
          ? 0
          : round(Math.max(3, (datum.failureCount / maximumFailures) * failureHeight))
      const rawWidth = ((datum.end - datum.start) / rangeDuration) * width
      const barWidth = Math.min(rawWidth, width, Math.max(3, Math.min(6, rawWidth * 0.26)))
      const point = points[index]!
      return {
        x: round(point.x - barWidth / 2),
        y: round(baseline - height),
        width: round(barWidth),
        height,
        value: datum.failureCount,
      }
    }),
    plotTop,
    baseline,
  }
}
