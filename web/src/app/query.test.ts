import { createAppQueryClient } from './query'

describe('createAppQueryClient', () => {
  it('disables automatic retries for queries and mutations', () => {
    const client = createAppQueryClient()

    expect(client.getDefaultOptions().queries?.retry).toBe(false)
    expect(client.getDefaultOptions().mutations?.retry).toBe(false)
  })
})
