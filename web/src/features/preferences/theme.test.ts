import { createThemeController, type ThemeControllerDependencies } from './theme'

function createDependencies(
  overrides: Partial<ThemeControllerDependencies> = {},
): ThemeControllerDependencies {
  return {
    documentElement: document.documentElement,
    storage: window.localStorage,
    matchMedia: window.matchMedia.bind(window),
    ...overrides,
  }
}

beforeEach(() => {
  document.documentElement.removeAttribute('data-theme')
})

describe('createThemeController', () => {
  it('removes data-theme when system is selected', () => {
    document.documentElement.dataset.theme = 'dark'
    const controller = createThemeController(createDependencies())

    controller.setTheme('system')

    expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
    expect(controller.theme.value).toBe('system')
    expect(window.localStorage.getItem('gpt-load.theme')).toBe('system')
  })

  it.each(['light', 'dark'] as const)('sets data-theme for explicit %s mode', (theme) => {
    const controller = createThemeController(createDependencies())

    controller.setTheme(theme)

    expect(document.documentElement.dataset.theme).toBe(theme)
    expect(controller.theme.value).toBe(theme)
  })

  it('restores only a valid stored theme', () => {
    window.localStorage.setItem('gpt-load.theme', 'dark')

    const controller = createThemeController(createDependencies())

    expect(controller.theme.value).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('keeps working in memory when storage access is denied', () => {
    const storage = {
      getItem: () => {
        throw new DOMException('denied')
      },
      setItem: () => {
        throw new DOMException('denied')
      },
    } as unknown as Storage

    const controller = createThemeController(createDependencies({ storage }))
    expect(() => controller.setTheme('dark')).not.toThrow()
    expect(controller.theme.value).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('degrades safely when matchMedia is unavailable', () => {
    const matchMedia = () => {
      throw new DOMException('unavailable')
    }

    expect(() => createThemeController(createDependencies({ matchMedia }))).not.toThrow()
    expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
  })

  it('removes its media-query listener on disposal', () => {
    const removeEventListener = vi.fn()
    const media = {
      addEventListener: vi.fn(),
      removeEventListener,
      matches: false,
      media: '(prefers-color-scheme: dark)',
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    } as unknown as MediaQueryList
    const controller = createThemeController(createDependencies({ matchMedia: () => media }))

    expect(media.addEventListener).toHaveBeenCalledWith('change', expect.any(Function))
    const listener = vi.mocked(media.addEventListener).mock.calls[0]?.[1]
    controller.dispose()

    expect(removeEventListener).toHaveBeenCalledWith('change', listener)
  })
})
