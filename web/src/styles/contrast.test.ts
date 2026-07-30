import './tokens.css'
import './base.css'
import baseStyles from './base.css?raw'
import tokenStyles from './tokens.css?raw'

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
    ['canvas', 'surface', 'text-muted', 'text-faint', 'border-control', 'action'].map((name) => [
      name,
      style.getPropertyValue(`--color-${name}`).trim().toLowerCase(),
    ]),
  )
}

describe('semantic token contrast', () => {
  const light = themeTokens(':root')
  const dark = themeTokens(":root[data-theme='dark']")

  it('keeps the approved Ledger light and dark semantic token values', () => {
    expect(light).toEqual({
      canvas: '#f8f8f6',
      surface: '#ffffff',
      'text-muted': '#4e545b',
      'text-faint': '#787f87',
      'border-control': '#787f87',
      action: '#1c4f6e',
    })
    expect(dark).toEqual({
      canvas: '#101317',
      surface: '#171b20',
      'text-muted': '#969ca3',
      'text-faint': '#6d747c',
      'border-control': '#6d747c',
      action: '#6fb2d6',
    })
  })

  it.each([
    ['light', light],
    ['dark', dark],
  ] as const)('%s text and control boundaries meet their minimum contrast', (_name, tokens) => {
    expect(contrast(tokens['text-muted']!, tokens.surface!)).toBeGreaterThanOrEqual(4.5)
    expect(contrast(tokens['text-faint']!, tokens.surface!)).toBeGreaterThanOrEqual(3)
    expect(contrast(tokens['border-control']!, tokens.surface!)).toBeGreaterThanOrEqual(3)
    expect(contrast(tokens.action!, tokens.surface!)).toBeGreaterThanOrEqual(3)
  })

  it('defines the complete Ledger semantic token layers without legacy aliases', () => {
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
      'color-neutral',
      'color-neutral-bg',
      'color-disabled',
      'font-sans',
      'font-serif',
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
    expect(style.getPropertyValue('--font-serif')).toContain('Iowan Old Style')
    expect(style.getPropertyValue('--radius-card').trim()).toBe('8px')
    expect(style.getPropertyValue('--content-max').trim()).toBe('1280px')
    for (const legacyName of [
      'color-page',
      'color-surface-secondary',
      'color-border',
      'color-primary',
      'color-primary-ink',
      'color-primary-soft',
    ]) {
      expect(style.getPropertyValue(`--${legacyName}`).trim(), legacyName).toBe('')
    }
    expect(tokenStyles.match(/^:root\s*\{/gm)).toHaveLength(1)
    expect(tokenStyles).not.toContain('#f7f6f3')
    expect(tokenStyles).not.toContain('#171613')
    expect(tokenStyles).not.toContain('#2f6db5')
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

  it('provides one global reduced-motion fallback for all animated descendants', () => {
    expect(baseStyles).toMatch(
      /@media \(prefers-reduced-motion: reduce\)\s*\{[\s\S]*\*,\s*\*::before,\s*\*::after\s*\{[\s\S]*transition-duration: 0\.01ms !important;/,
    )
  })
})
