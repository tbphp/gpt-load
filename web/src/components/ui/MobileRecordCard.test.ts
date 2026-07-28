import { mount } from '@vue/test-utils'

import MobileRecordCard from './MobileRecordCard.vue'

describe('MobileRecordCard', () => {
  it('renders one semantic record with header, fields and actions', () => {
    const wrapper = mount(MobileRecordCard, {
      props: {
        label: 'Access key alpha',
      },
      slots: {
        header: '<strong>alpha</strong>',
        default: '<dl><dt>Status</dt><dd>Active</dd></dl>',
        actions: '<button type="button">Edit</button>',
      },
    })

    expect(wrapper.element.tagName).toBe('ARTICLE')
    expect(wrapper.attributes('aria-label')).toBe('Access key alpha')
    expect(wrapper.get('strong').text()).toBe('alpha')
    expect(wrapper.get('button').text()).toBe('Edit')
  })
})
