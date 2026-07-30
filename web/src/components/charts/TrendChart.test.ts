import { mount } from '@vue/test-utils'

import TrendChart from './TrendChart.vue'
import trendChartSource from './TrendChart.vue?raw'

const series = [
  {
    bucket_start: '2026-07-29T00:00:00.000Z',
    bucket_end: '2026-07-29T01:00:00.000Z',
    request_count: 8,
    failure_count: 0,
  },
  {
    bucket_start: '2026-07-29T01:00:00.000Z',
    bucket_end: '2026-07-29T02:00:00.000Z',
    request_count: 12,
    failure_count: 2,
  },
]

function mountChart(input = series) {
  return mount(TrendChart, {
    props: {
      series: input,
      title: 'Request trend',
      description: 'Requests and failed requests by returned bucket.',
      emptyLabel: 'No returned buckets.',
      requestLabel: 'Requests',
      failureLabel: 'Failed',
      locale: 'en-US',
      nowLabel: 'Now',
      rateSuffix: '/h',
      failureStripLabel: 'Failed requests · hourly count',
      rangeStart: '2026-07-29T00:00:00.000Z',
      rangeEnd: '2026-07-30T00:00:00.000Z',
    },
  })
}

describe('TrendChart', () => {
  it('renders one accessible SVG with separate request geometry and failure bars', () => {
    const wrapper = mountChart()
    const svg = wrapper.get('svg[role="img"]')

    expect(svg.attributes('role')).toBe('img')
    expect(svg.attributes('aria-labelledby')).toMatch(
      /^trend-chart-title-.+ trend-chart-description-.+$/,
    )
    expect(wrapper.get('title').text()).toBe('Request trend')
    expect(wrapper.get('desc').text()).toBe('Requests and failed requests by returned bucket.')
    expect(wrapper.get('[data-test="trend-request-area"]').attributes('d')).not.toBe('')
    expect(wrapper.get('[data-test="trend-request-path"]').attributes('d')).not.toBe('')
    expect(wrapper.find('[data-test="trend-request-marker"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-test="trend-failure-bar"]')).toHaveLength(1)
  })

  it('shows editorial ticks and the last rate without exposing raw ISO timestamps', () => {
    const wrapper = mountChart()
    const ticks = wrapper.get('[data-test="trend-time-axis"]')

    expect(ticks.text()).toContain('00:00')
    expect(ticks.text()).toContain('12:00')
    expect(ticks.text()).toContain('Now')
    expect(wrapper.get('[data-test="trend-last-value"]').text()).toBe('12/h')
    expect(wrapper.get('[data-test="trend-failure-label"]').text()).toBe(
      'Failed requests · hourly count',
    )
    expect(wrapper.text()).not.toContain('2026-07-29T02:00:00.000Z')
  })

  it('renders a named empty state instead of fabricating a chart', () => {
    const wrapper = mountChart([])

    expect(wrapper.find('svg').exists()).toBe(false)
    expect(wrapper.get('[data-test="trend-chart-empty"]').attributes('role')).toBe('status')
    expect(wrapper.text()).toContain('No returned buckets.')
  })

  it('has no tooltip dependency or information-bearing animation', () => {
    expect(trendChartSource).not.toMatch(/tooltip|<animate|animation:|transition:/i)
    expect(trendChartSource).toContain("from './trend-chart'")
    expect(trendChartSource).toMatch(/\.trend-chart__request-graphic\s*\{[\s\S]*height: 185px;/)
    expect(trendChartSource).toMatch(/\.trend-chart\s*\{[\s\S]*padding-bottom: var\(--space-2\);/)
    expect(trendChartSource).toMatch(/\.trend-chart__axis\s*\{[\s\S]*margin-top: var\(--space-4\);/)
    expect(trendChartSource).toContain(
      'color-mix(in srgb, var(--color-action) 12%, var(--color-canvas))',
    )
    expect(trendChartSource).toMatch(/\.trend-chart__failure-strip\s*\{[\s\S]*display: grid;/)
    expect(trendChartSource).not.toMatch(
      /\.trend-chart__failure-label\s*\{[\s\S]*position: absolute;/,
    )
  })
})
