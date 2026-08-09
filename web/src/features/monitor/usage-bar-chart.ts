export interface UsageBarDetail {
  label: string
  display: string
}

export interface UsageBarDatum {
  bucket_start_ms: number
  bucket_end_ms: number
  primary_value: number
  secondary_value: number
  primary_display?: string
  secondary_display?: string
  details?: readonly UsageBarDetail[]
}

export interface UsageBarPoint {
  x: number
  y: number
}

export interface UsageBar {
  x: number
  y: number
  width: number
  height: number
  value: number
}

export interface UsageBarGeometry {
  points: UsageBarPoint[]
  primaryBars: UsageBar[]
  secondaryBars: UsageBar[]
  plotTop: number
  baseline: number
}

interface ParsedUsageBarDatum {
  start: number
  end: number
  primaryValue: number
  secondaryValue: number
}

function round(value: number): number {
  return Math.round(value * 100) / 100
}

function validDimension(value: number): boolean {
  return Number.isFinite(value) && value > 0
}

function parseDatum(datum: UsageBarDatum): ParsedUsageBarDatum | undefined {
  const {
    bucket_start_ms: start,
    bucket_end_ms: end,
    primary_value: primaryValue,
    secondary_value: secondaryValue,
  } = datum
  if (
    !Number.isSafeInteger(start) ||
    start < 0 ||
    !Number.isSafeInteger(end) ||
    end <= start ||
    !Number.isSafeInteger(primaryValue) ||
    primaryValue < 0 ||
    !Number.isSafeInteger(secondaryValue) ||
    secondaryValue < 0
  ) {
    return undefined
  }
  return { start, end, primaryValue, secondaryValue }
}

export function isUsageBarSeriesUsable(
  series: readonly UsageBarDatum[],
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

export function completeUsageBarSeries(
  series: readonly UsageBarDatum[],
  rangeStart: number,
  rangeEnd: number,
): UsageBarDatum[] {
  if (series.length === 0 || !isUsageBarSeriesUsable(series, rangeStart, rangeEnd)) {
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
  const result: UsageBarDatum[] = []
  for (let bucketStart = rangeStart; bucketStart < rangeEnd; bucketStart += bucketDuration) {
    result.push(
      byStart.get(bucketStart) ?? {
        bucket_start_ms: bucketStart,
        bucket_end_ms: bucketStart + bucketDuration,
        primary_value: 0,
        secondary_value: 0,
      },
    )
  }
  return result
}

export function buildUsageBarGeometry(
  series: readonly UsageBarDatum[],
  width: number,
  chartHeight: number,
  rangeStart: number,
  rangeEnd: number,
  grouped: boolean,
): UsageBarGeometry {
  const plotTop = validDimension(chartHeight) ? Math.min(14, chartHeight * 0.1) : 0
  const plotBottomInset = validDimension(chartHeight) ? Math.min(8, chartHeight * 0.1) : 0
  const baseline = validDimension(chartHeight) ? chartHeight - plotBottomInset : 0
  const empty: UsageBarGeometry = {
    points: [],
    primaryBars: [],
    secondaryBars: [],
    plotTop,
    baseline,
  }
  if (
    !validDimension(width) ||
    !validDimension(chartHeight) ||
    series.length === 0 ||
    !isUsageBarSeriesUsable(series, rangeStart, rangeEnd)
  ) {
    return empty
  }

  const parsed = series.map(parseDatum) as ParsedUsageBarDatum[]
  const rangeDuration = rangeEnd - rangeStart
  const maximumValue =
    Math.max(
      ...parsed.map((datum) => datum.primaryValue),
      ...(grouped ? parsed.map((datum) => datum.secondaryValue) : [0]),
    ) * 1.08
  const availableHeight = baseline - plotTop
  const scaledHeight = (value: number) =>
    maximumValue === 0 || value === 0
      ? 0
      : round(Math.max(2, Math.min(availableHeight, (value / maximumValue) * availableHeight)))

  const points = parsed.map<UsageBarPoint>((datum) => {
    const value = grouped ? Math.max(datum.primaryValue, datum.secondaryValue) : datum.primaryValue
    return {
      x: round(
        ((datum.start - rangeStart + (datum.end - datum.start) / 2) / rangeDuration) * width,
      ),
      y: round(baseline - scaledHeight(value)),
    }
  })
  const groupWidths = parsed.map((datum) => {
    const rawWidth = ((datum.end - datum.start) / rangeDuration) * width
    return Math.min(rawWidth, width, Math.max(4, Math.min(24, rawWidth * 0.52)))
  })

  const primaryBars = parsed.map((datum, index) => {
    const groupWidth = groupWidths[index]!
    const gap = grouped ? Math.min(0.8, Math.max(0.4, groupWidth * 0.035)) : 0
    const barWidth = grouped ? (groupWidth - gap) / 2 : groupWidth
    const point = points[index]!
    const height = scaledHeight(datum.primaryValue)
    return {
      x: round(grouped ? point.x - gap / 2 - barWidth : point.x - barWidth / 2),
      y: round(baseline - height),
      width: round(barWidth),
      height,
      value: datum.primaryValue,
    }
  })
  const secondaryBars = grouped
    ? parsed.map((datum, index) => {
        const groupWidth = groupWidths[index]!
        const gap = Math.min(0.8, Math.max(0.4, groupWidth * 0.035))
        const barWidth = (groupWidth - gap) / 2
        const point = points[index]!
        const height = scaledHeight(datum.secondaryValue)
        return {
          x: round(point.x + gap / 2),
          y: round(baseline - height),
          width: round(barWidth),
          height,
          value: datum.secondaryValue,
        }
      })
    : []

  return {
    points,
    primaryBars,
    secondaryBars,
    plotTop,
    baseline,
  }
}
