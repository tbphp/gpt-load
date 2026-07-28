import { mount } from '@vue/test-utils'

import PageHeader from './PageHeader.vue'

describe('PageHeader', () => {
  it('puts the configured id and programmatic focus target on the H1', () => {
    const wrapper = mount(PageHeader, {
      props: { id: 'settings-title', title: 'Settings', description: 'Configure the service.' },
    })

    expect(wrapper.get('header').attributes('id')).toBeUndefined()
    expect(wrapper.get('h1').attributes()).toMatchObject({
      id: 'settings-title',
      tabindex: '-1',
    })
  })
})
