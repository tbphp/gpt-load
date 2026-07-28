import { mount } from '@vue/test-utils'

import EmptyState from './EmptyState.vue'
import QueryFeedback from './QueryFeedback.vue'

describe('feedback contracts', () => {
  it('renders indeterminate query state with an explicit recovery action', async () => {
    const wrapper = mount(QueryFeedback, {
      props: {
        state: 'indeterminate',
        message: 'Result not confirmed.',
        retryLabel: 'Check result',
      },
    })

    expect(wrapper.attributes('role')).toBe('status')
    expect(wrapper.text()).toContain('Result not confirmed.')
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
  })

  it('lets the owner select the empty-state heading level', () => {
    const wrapper = mount(EmptyState, {
      props: {
        title: 'No matching entries',
        description: 'Change the filters and try again.',
        headingAs: 'h3',
      },
    })

    expect(wrapper.get('h3').text()).toBe('No matching entries')
    expect(wrapper.find('h2').exists()).toBe(false)
  })
})
