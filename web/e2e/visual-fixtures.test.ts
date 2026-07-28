import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

import {
  visualClock,
  visualFixtureData,
  visualLocales,
  visualScenarios,
  visualThemes,
  visualViewports,
} from './visual-fixtures'

const businessFlowSource = readFileSync(resolve('e2e/business-flows.spec.ts'), 'utf8')

describe('deterministic visual fixtures', () => {
  it('freezes the approved scenario, viewport, theme, and locale matrix', () => {
    expect(visualScenarios).toEqual([
      'home-normal',
      'home-anomaly',
      'access-keys-long',
      'model-prices-mixed',
      'settings-dirty',
      'usage-quality',
      'logs-signal-path',
    ])
    expect(visualViewports).toEqual([
      { width: 375, height: 812 },
      { width: 768, height: 900 },
      { width: 1024, height: 900 },
      { width: 1440, height: 900 },
    ])
    expect(visualThemes).toEqual(['light', 'dark'])
    expect(visualLocales).toEqual(['en-US', 'zh-CN'])
  })

  it('uses stable public-safe values without plaintext credentials', () => {
    expect(visualClock).toBe('2026-07-29T00:00:00.000Z')
    expect(visualFixtureData.requestId).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    )
    expect(visualFixtureData.accessKeySuffix).toMatch(/^[0-9a-f]{4}$/)
    expect(visualFixtureData.longIdentifier.length).toBeGreaterThan(96)

    const serialized = JSON.stringify(visualFixtureData)
    expect(serialized).not.toMatch(/sk-gl-[0-9a-z]{16,}/i)
    expect(serialized).not.toContain('Bearer ')
  })

  it('binds every named scenario and locale to the executable browser journey', () => {
    for (const scenario of visualScenarios) {
      expect(businessFlowSource).toContain(`visualScenarioLabel('${scenario}')`)
    }
    expect(businessFlowSource).toContain('for (const locale of visualLocales)')
  })
})
