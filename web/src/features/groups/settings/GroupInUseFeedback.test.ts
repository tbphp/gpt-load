import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

import { createTestAppI18n as createAppI18n } from '@/test/i18n'

import GroupInUseFeedback from './GroupInUseFeedback.vue'

describe('GroupInUseFeedback', () => {
  it('renders the exact structured AccessKey references and management action', () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/access-keys',
          name: 'access-keys',
          component: { template: '<div />' },
        },
      ],
    })
    const wrapper = mount(GroupInUseFeedback, {
      props: {
        references: [
          { id: 11, name: 'Production clients' },
          { id: 19, name: 'Canary clients' },
        ],
      },
      global: { plugins: [createAppI18n(undefined, 'en-US').plugin, router] },
    })

    expect(wrapper.get('[role="alert"]').text()).toContain('Production clients')
    expect(wrapper.get('[role="alert"]').text()).toContain('#11')
    expect(wrapper.get('[role="alert"]').text()).toContain('Canary clients')
    expect(wrapper.get('[role="alert"]').text()).toContain('#19')
    expect(wrapper.get('[data-test="group-in-use-access-keys"]').attributes('href')).toBe(
      '/access-keys',
    )
  })
})
