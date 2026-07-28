import { mount } from '@vue/test-utils'

import AppDateTime from './AppDateTime.vue'

describe('AppDateTime', () => {
  it('always exposes the source instant with localized absolute time and timezone', () => {
    const wrapper = mount(AppDateTime, {
      props: {
        instant: '2026-07-29T04:30:00.000Z',
        locale: 'en-US',
        timeZone: 'Asia/Shanghai',
        relativeTo: new Date('2026-07-29T04:31:00.000Z'),
      },
    })

    expect(wrapper.get('time').attributes('datetime')).toBe('2026-07-29T04:30:00.000Z')
    expect(wrapper.get('time').text()).toContain('GMT+8')
    expect(wrapper.text()).toContain('1 minute ago')
  })

  it('fails honestly by rendering an invalid source value without a datetime attribute', () => {
    const wrapper = mount(AppDateTime, {
      props: {
        instant: 'not-an-instant',
        locale: 'en-US',
      },
    })

    expect(wrapper.text()).toBe('not-an-instant')
    expect(wrapper.find('time').exists()).toBe(false)
  })
})
