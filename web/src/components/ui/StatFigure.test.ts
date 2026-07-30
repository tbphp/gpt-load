import { mount } from '@vue/test-utils'

import StatFigure from './StatFigure.vue'
import statFigureSource from './StatFigure.vue?raw'

describe('StatFigure', () => {
  it('renders one label, primary value and optional detail without imposing a heading level', () => {
    const wrapper = mount(StatFigure, {
      props: {
        label: 'Success rate · last 24h',
        value: '99.1%',
        detail: '16,204 requests · 146 failed',
      },
    })

    expect(wrapper.get('[data-stat-label]').text()).toBe('Success rate · last 24h')
    expect(wrapper.get('[data-stat-value]').text()).toBe('99.1%')
    expect(wrapper.get('[data-stat-detail]').text()).toBe('16,204 requests · 146 failed')
    expect(wrapper.find('h1, h2, h3, h4, h5, h6').exists()).toBe(false)
  })

  it('omits an empty detail row', () => {
    const wrapper = mount(StatFigure, {
      props: { label: 'Groups', value: 14 },
    })

    expect(wrapper.get('[data-stat-value]').text()).toBe('14')
    expect(wrapper.find('[data-stat-detail]').exists()).toBe(false)
  })

  it('caps the Ledger primary value at 48px', () => {
    expect(statFigureSource).toMatch(
      /\.stat-figure__value\s*\{[\s\S]*font-size: clamp\(2\.25rem, 4vw, 3rem\);/,
    )
    expect(statFigureSource).not.toContain('3.5rem')
  })
})
