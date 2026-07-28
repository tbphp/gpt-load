import { mount } from '@vue/test-utils'

import Surface from './Surface.vue'

describe('Surface', () => {
  it('renders the selected semantic element and visual variant', () => {
    const wrapper = mount(Surface, {
      props: {
        as: 'aside',
        variant: 'sunken',
        padded: false,
      },
      slots: {
        default: 'Recovery details',
      },
    })

    expect(wrapper.element.tagName).toBe('ASIDE')
    expect(wrapper.classes()).toEqual(
      expect.arrayContaining(['surface', 'surface--sunken', 'surface--flush']),
    )
  })
})
