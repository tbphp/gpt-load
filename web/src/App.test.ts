import { mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'

import App from './App.vue'
import { createAppRouter } from './app/router'

async function mountAt(path: string) {
  const router = createAppRouter(createMemoryHistory())
  await router.push(path)
  await router.isReady()
  return mount(App, { global: { plugins: [router] } })
}

describe('App', () => {
  it('renders the S1 home placeholder', async () => {
    const wrapper = await mountAt('/')

    expect(wrapper.get('h1').text()).toBe('管理界面基础已就绪')
    expect(wrapper.get('nav').attributes('aria-label')).toBe('主导航')
  })

  it('renders the login placeholder on the explicit login route', async () => {
    const wrapper = await mountAt('/login')

    expect(wrapper.get('h1').text()).toBe('登录')
  })
})
