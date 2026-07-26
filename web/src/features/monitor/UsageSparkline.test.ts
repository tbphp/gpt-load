import { mount } from '@vue/test-utils'

import type { UsageReportDto } from '@/api/control/usage'

import UsageSparkline from './UsageSparkline.vue'

const range = {
  from: '2026-07-27T00:00:00.000Z',
  to: '2026-07-27T04:00:00.000Z',
  granularity: 'hour' as const,
}

function point(bucketStart: string, requestCount: number) {
  const start = new Date(bucketStart)
  return {
    bucket_start: bucketStart,
    bucket_end: new Date(start.getTime() + 60 * 60 * 1000).toISOString(),
    request_count: requestCount,
  }
}

function mountSparkline(
  series: Array<{ bucket_start: string; bucket_end: string; request_count: number }>,
  inputRange: UsageReportDto['range'] = range,
) {
  return mount(UsageSparkline, {
    props: {
      range: inputRange,
      series,
      title: 'Request trend',
      description: 'Persisted requests per UTC bucket.',
      emptyLabel: 'No persisted requests in this range.',
    },
  })
}

describe('UsageSparkline', () => {
  it('renders an accessible empty state without fabricating a chart', () => {
    const wrapper = mountSparkline([])

    expect(wrapper.find('svg').exists()).toBe(false)
    expect(wrapper.get('[data-test="usage-sparkline-empty"]').attributes('role')).toBe('status')
    expect(wrapper.text()).toContain('No persisted requests in this range.')
  })

  it('renders a single point as one visible marker without inventing a line segment', () => {
    const wrapper = mountSparkline([point('2026-07-27T02:00:00.000Z', 8)])

    expect(wrapper.find('[data-test="usage-sparkline-path"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="usage-sparkline-marker"]').attributes()).toMatchObject({
      cx: '120',
      cy: '4',
    })
  })

  it('draws continuous hourly and daily buckets as one segment', () => {
    const hourly = mountSparkline([
      point('2026-07-27T00:00:00.000Z', 2),
      point('2026-07-27T01:00:00.000Z', 4),
      point('2026-07-27T02:00:00.000Z', 3),
    ])
    const daily = mountSparkline(
      [
        point('2026-07-27T00:00:00.000Z', 2),
        point('2026-07-28T00:00:00.000Z', 4),
        point('2026-07-29T00:00:00.000Z', 3),
      ],
      {
        from: '2026-07-27T00:00:00.000Z',
        to: '2026-07-30T00:00:00.000Z',
        granularity: 'day',
      },
    )

    const hourlyPath = hourly.get('[data-test="usage-sparkline-path"]').attributes('d')
    const dailyPath = daily.get('[data-test="usage-sparkline-path"]').attributes('d')

    expect(hourlyPath?.match(/\bM\b/g)).toHaveLength(1)
    expect(dailyPath?.match(/\bM\b/g)).toHaveLength(1)
  })

  it('starts a new subpath when an hourly interval is absent', () => {
    const wrapper = mountSparkline([
      point('2026-07-27T00:00:00.000Z', 2),
      point('2026-07-27T02:00:00.000Z', 4),
    ])

    expect(wrapper.get('[data-test="usage-sparkline-path"]').attributes('d')).toBe(
      'M 4 32 L 4 32 M 120 4 L 120 4',
    )
  })

  it('uses the complete UTC response window for x positions and keeps constant values stable', () => {
    const wrapper = mountSparkline([
      point('2026-07-27T01:00:00.000Z', 5),
      point('2026-07-27T03:00:00.000Z', 5),
    ])

    expect(wrapper.get('[data-test="usage-sparkline-path"]').attributes('d')).toBe(
      'M 62 4 L 62 4 M 178 4 L 178 4',
    )
  })

  it('uses a responsive viewBox with an accessible SVG name and no tooltip or animation', () => {
    const wrapper = mountSparkline([point('2026-07-27T02:00:00.000Z', 1)])
    const svg = wrapper.get('svg')

    expect(svg.attributes()).toMatchObject({
      role: 'img',
      viewBox: '0 0 240 64',
      'aria-labelledby': expect.stringMatching(/^usage-sparkline-title-/),
    })
    expect(wrapper.get('title').text()).toBe('Request trend')
    expect(wrapper.get('desc').text()).toBe('Persisted requests per UTC bucket.')
    expect(wrapper.html()).not.toContain('<animate')
    expect(wrapper.html()).not.toContain('<title="')
  })
})
