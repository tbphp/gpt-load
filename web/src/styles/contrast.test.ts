import './tokens.css'
import './base.css'

function channel(value: number): number {
  const normalized = value / 255
  return normalized <= 0.04045 ? normalized / 12.92 : Math.pow((normalized + 0.055) / 1.055, 2.4)
}

function luminance(hex: string): number {
  const channels = [1, 3, 5].map((offset) =>
    channel(Number.parseInt(hex.slice(offset, offset + 2), 16)),
  )
  return 0.2126 * channels[0]! + 0.7152 * channels[1]! + 0.0722 * channels[2]!
}

function contrast(foreground: string, background: string): number {
  const values = [luminance(foreground), luminance(background)].sort((a, b) => b - a)
  return (values[0]! + 0.05) / (values[1]! + 0.05)
}

function styleRule(selector: string): CSSStyleDeclaration {
  for (const sheet of document.styleSheets) {
    for (const rule of sheet.cssRules) {
      if (rule instanceof CSSStyleRule && rule.selectorText === selector) return rule.style
    }
  }
  throw new Error(`missing style rule ${selector}`)
}

function themeTokens(selector: string): Record<string, string> {
  const style = styleRule(selector)
  return Object.fromEntries(
    ['canvas', 'surface', 'text-faint', 'border-control', 'action'].map((name) => [
      name,
      style.getPropertyValue(`--color-${name}`).trim().toLowerCase(),
    ]),
  )
}

describe('semantic token contrast', () => {
  const light = themeTokens(':root')
  const dark = themeTokens(":root[data-theme='dark']")

  it('keeps the approved semantic token values', () => {
    expect(light['text-faint']).toBe('#746f67')
    expect(light['border-control']).toBe('#918b81')
    expect(dark['text-faint']).toBe('#938c80')
    expect(dark['border-control']).toBe('#716a5f')
  })

  it.each([
    ['light', light],
    ['dark', dark],
  ] as const)('%s text and control boundaries meet their minimum contrast', (_name, tokens) => {
    expect(contrast(tokens['text-faint']!, tokens.surface!)).toBeGreaterThanOrEqual(4.5)
    expect(contrast(tokens['text-faint']!, tokens.canvas!)).toBeGreaterThanOrEqual(4.5)
    expect(contrast(tokens['border-control']!, tokens.surface!)).toBeGreaterThanOrEqual(3)
    expect(contrast(tokens.action!, tokens.surface!)).toBeGreaterThanOrEqual(3)
  })

  it('defines the complete approved semantic token layers', () => {
    const style = styleRule(':root')
    const names = [
      'color-canvas',
      'color-surface',
      'color-surface-raised',
      'color-surface-sunken',
      'color-text',
      'color-text-muted',
      'color-text-faint',
      'color-text-inverse',
      'color-border-subtle',
      'color-border-control',
      'color-border-strong',
      'color-action',
      'color-action-hover',
      'color-action-soft',
      'color-focus',
      'color-success',
      'color-warning',
      'color-danger',
      'color-disabled',
      'font-sans',
      'font-mono',
      'text-xs',
      'text-sm',
      'text-md',
      'text-lg',
      'text-xl',
      'text-display',
      'line-compact',
      'line-normal',
      'line-relaxed',
      'radius-card',
      'radius-control',
      'radius-tag',
      'control-sm',
      'control-md',
      'control-lg',
      'touch-target',
      'content-max',
      'page-gutter',
      'section-gap',
      'collection-row-height',
      'duration-fast',
      'duration-normal',
      'easing-standard',
    ]

    for (const name of names) {
      expect(style.getPropertyValue(`--${name}`).trim(), name).not.toBe('')
    }
    expect(style.getPropertyValue('--font-mono')).toContain('ui-monospace')
  })

  it('keeps standard motion within the approved 150–200ms range', () => {
    const style = styleRule(':root')
    for (const token of ['--duration-fast', '--duration-normal']) {
      const value = style.getPropertyValue(token).trim()
      expect(value).toMatch(/^\d+ms$/)
      const milliseconds = Number.parseInt(value, 10)
      expect(milliseconds, token).toBeGreaterThanOrEqual(150)
      expect(milliseconds, token).toBeLessThanOrEqual(200)
    }
  })

  it('uses the control border token on shared form controls', () => {
    expect(styleRule('.form-field input').cssText).toContain('var(--color-border-control)')
  })
})
