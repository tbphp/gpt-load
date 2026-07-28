export const visualFixtureVersion = 1
export const visualClock = '2026-07-29T00:00:00.000Z'

export const visualScenarios = [
  'home-normal',
  'home-anomaly',
  'access-keys-long',
  'model-prices-mixed',
  'settings-dirty',
  'usage-quality',
  'logs-signal-path',
] as const

export type VisualScenario = (typeof visualScenarios)[number]

const visualScenarioDescriptions: Record<VisualScenario, string> = {
  'home-normal': 'Home normal operation',
  'home-anomaly': 'Home usage and health anomaly',
  'access-keys-long': 'AccessKey long record',
  'model-prices-mixed': 'Model prices mixed values',
  'settings-dirty': 'Settings dirty state',
  'usage-quality': 'Usage quality signals',
  'logs-signal-path': 'Logs signal path',
}

export function visualScenarioLabel(scenario: VisualScenario): string {
  return `[visual:${scenario}] ${visualScenarioDescriptions[scenario]}`
}

export const visualViewports = [
  { width: 375, height: 812 },
  { width: 768, height: 900 },
  { width: 1024, height: 900 },
  { width: 1440, height: 900 },
] as const

export const visualThemes = ['light', 'dark'] as const
export const visualLocales = ['en-US', 'zh-CN'] as const

export const visualFixtureData = Object.freeze({
  requestId: '5297f18d-3ca7-42d9-8a5b-2ce08ccd1b01',
  accessKeySuffix: 'a11e',
  groupName: 'Visual Fixture OpenAI Group',
  modelName: 'visual-fixture-model-with-a-stable-long-identifier',
  longIdentifier:
    'visual-fixture-client-segment-segment-segment-segment-segment-segment-segment-segment-segment-segment-segment-segment',
  observedAt: visualClock,
})
