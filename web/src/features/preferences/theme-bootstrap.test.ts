import bootstrapSource from '../../../public/theme-bootstrap.js?raw'

function runBootstrap(storage: Pick<Storage, 'getItem'> = window.localStorage): void {
  const bootstrap = new Function('window', 'document', bootstrapSource)
  bootstrap({ localStorage: storage }, document)
}

beforeEach(() => {
  window.localStorage.clear()
  document.documentElement.removeAttribute('data-theme')
})

describe('theme bootstrap', () => {
  it.each(['light', 'dark'] as const)(
    'applies explicit stored %s theme before application mount',
    (theme) => {
      window.localStorage.setItem('gpt-load.theme', theme)

      runBootstrap()

      expect(document.documentElement.dataset.theme).toBe(theme)
    },
  )

  it.each(['system', 'unexpected', null])(
    'removes data-theme for non-explicit preference %s',
    (stored) => {
      document.documentElement.dataset.theme = 'dark'
      if (stored !== null) window.localStorage.setItem('gpt-load.theme', stored)

      runBootstrap()

      expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
    },
  )

  it('fails safely when storage is unavailable', () => {
    document.documentElement.dataset.theme = 'dark'

    expect(() =>
      runBootstrap({
        getItem() {
          throw new DOMException('denied')
        },
      }),
    ).not.toThrow()
    expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
  })
})
