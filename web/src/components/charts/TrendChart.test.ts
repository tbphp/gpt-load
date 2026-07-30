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
    },
  })
}

describe('TrendChart', () => {
  it('renders one accessible SVG with separate request geometry and failure bars', () => {
    const wrapper = mountChart()
    const svg = wrapper.get('svg')

    expect(svg.attributes('role')).toBe('img')
    expect(svg.attributes('aria-labelledby')).toMatch(
      /^trend-chart-title-.+ trend-chart-description-.+$/,
    )
    expect(wrapper.get('title').text()).toBe('Request trend')
    expect(wrapper.get('desc').text()).toBe('Requests and failed requests by returned bucket.')
    expect(wrapper.get('[data-test="trend-request-area"]').attributes('d')).not.toBe('')
    expect(wrapper.get('[data-test="trend-request-path"]').attributes('d')).not.toBe('')
    expect(wrapper.findAll('[data-test="trend-failure-bar"]')).toHaveLength(1)
  })

  it('keeps the last returned bucket summary visible outside the SVG', () => {
    const wrapper = mountChart()
    const summary = wrapper.get('[data-test="trend-last-bucket"]')

    expect(summary.text()).toContain('2026-07-29T02:00:00.000Z')
    expect(summary.text()).toContain('Requests 12')
    expect(summary.text()).toContain('Failed 2')
    expect(summary.get('time').attributes('datetime')).toBe('2026-07-29T02:00:00.000Z')
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
  })
})
