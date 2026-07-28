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
    ['page', 'surface', 'text-faint', 'border-control', 'primary'].map((name) => [
      name,
      style.getPropertyValue(`--color-${name}`).trim().toLowerCase(),
    ]),
  )
}

describe('semantic token contrast', () => {
  const light = themeTokens(':root')
  const dark = themeTokens(":root[data-theme='dark']")

  it.each([
    ['light', light],
    ['dark', dark],
  ] as const)('%s text and control boundaries meet their minimum contrast', (_name, tokens) => {
    expect(contrast(tokens['text-faint']!, tokens.surface!)).toBeGreaterThanOrEqual(4.5)
    expect(contrast(tokens['text-faint']!, tokens.page!)).toBeGreaterThanOrEqual(4.5)
    expect(contrast(tokens['border-control']!, tokens.surface!)).toBeGreaterThanOrEqual(3)
    expect(contrast(tokens.primary!, tokens.surface!)).toBeGreaterThanOrEqual(3)
  })

  it('uses the control border token on shared form controls', () => {
    expect(styleRule('.form-field input').cssText).toContain('var(--color-border-control)')
  })
})
