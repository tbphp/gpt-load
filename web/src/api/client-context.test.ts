import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

import { apiClientKey, useApiClient } from './client-context'
import type { ApiClient } from './client'

let injected: ApiClient | undefined
const ApiClientConsumer = defineComponent({
  setup() {
    injected = useApiClient()
    return {}
  },
  template: '<div />',
})

describe('ApiClient injection', () => {
  it('fails fast when ApiClient is not provided', () => {
    expect(() => mount(ApiClientConsumer)).toThrow('API_CLIENT_NOT_PROVIDED')
  })

  it('returns the provided ApiClient', () => {
    const client: ApiClient = { request: vi.fn() }

    mount(ApiClientConsumer, {
      global: { provide: { [apiClientKey as symbol]: client } },
    })

    expect(injected).toBe(client)
  })
})
