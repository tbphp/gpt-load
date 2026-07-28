import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

import {
  visualClock,
  visualFixtureData,
  visualLocales,
  visualScenarioCases,
  visualScenarios,
  visualThemes,
  visualViewports,
} from './visual-fixtures'

const visualScenarioSource = readFileSync(resolve('e2e/visual-scenarios.spec.ts'), 'utf8')

describe('deterministic visual fixtures', () => {
  it('freezes the approved scenario, viewport, theme, and locale matrix', () => {
    expect(visualScenarios).toEqual([
      'home-normal',
      'home-anomaly',
      'home-empty-error',
      'access-keys-long',
      'access-key-operation',
      'model-prices-mixed',
      'settings-dirty',
      'settings-validation',
      'usage-quality',
      'logs-signal-path',
      'inspector-routing',
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

  it('binds every scenario and each approved render dimension to executable cases', () => {
    for (const scenario of visualScenarios) {
      expect(visualScenarioCases.some((candidate) => candidate.scenario === scenario)).toBe(true)
    }
    for (const viewport of visualViewports) {
      expect(visualScenarioCases.some((candidate) => candidate.viewport === viewport)).toBe(true)
    }
    for (const locale of visualLocales) {
      expect(visualScenarioCases.some((candidate) => candidate.locale === locale)).toBe(true)
    }
    for (const theme of visualThemes) {
      expect(visualScenarioCases.some((candidate) => candidate.theme === theme)).toBe(true)
    }

    expect(visualScenarioSource).toContain('installVisualApi')
    expect(visualScenarioSource).toContain('captureVisualScenario')
  })
})
