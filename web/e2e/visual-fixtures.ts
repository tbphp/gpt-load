export const visualFixtureVersion = 1
export const visualClock = '2026-07-29T00:00:00.000Z'

export const visualScenarios = [
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
] as const

export type VisualScenario = (typeof visualScenarios)[number]

const visualScenarioDescriptions: Record<VisualScenario, string> = {
  'home-normal': 'Home normal operation',
  'home-anomaly': 'Home usage and health anomaly',
  'home-empty-error': 'Home empty and unavailable resources',
  'access-keys-long': 'AccessKey long record',
  'access-key-operation': 'AccessKey indeterminate operation notice',
  'model-prices-mixed': 'Model prices mixed values',
  'settings-dirty': 'Settings dirty state',
  'settings-validation': 'Settings validation summary',
  'usage-quality': 'Usage quality signals',
  'logs-signal-path': 'Logs signal path',
  'inspector-routing': 'Route inspector result',
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

export interface VisualScenarioCase {
  id: string
  scenario: VisualScenario
  path: string
  viewport: (typeof visualViewports)[number]
  theme: (typeof visualThemes)[number]
  locale: (typeof visualLocales)[number]
}

export const visualScenarioCases: readonly VisualScenarioCase[] = [
  {
    id: 'home-normal-desktop-en-light',
    scenario: 'home-normal',
    path: '/',
    viewport: visualViewports[3],
    theme: 'light',
    locale: 'en-US',
  },
  {
    id: 'home-normal-mobile-zh-dark',
    scenario: 'home-normal',
    path: '/',
    viewport: visualViewports[0],
    theme: 'dark',
    locale: 'zh-CN',
  },
  {
    id: 'home-anomaly-tablet-en-dark',
    scenario: 'home-anomaly',
    path: '/',
    viewport: visualViewports[2],
    theme: 'dark',
    locale: 'en-US',
  },
  {
    id: 'home-empty-error-tablet-zh-light',
    scenario: 'home-empty-error',
    path: '/',
    viewport: visualViewports[1],
    theme: 'light',
    locale: 'zh-CN',
  },
  {
    id: 'access-keys-long-desktop-en-light',
    scenario: 'access-keys-long',
    path: '/access-keys',
    viewport: visualViewports[3],
    theme: 'light',
    locale: 'en-US',
  },
  {
    id: 'access-keys-long-mobile-zh-dark',
    scenario: 'access-keys-long',
    path: '/access-keys',
    viewport: visualViewports[0],
    theme: 'dark',
    locale: 'zh-CN',
  },
  {
    id: 'access-key-operation-tablet-en-light',
    scenario: 'access-key-operation',
    path: '/access-keys',
    viewport: visualViewports[1],
    theme: 'light',
    locale: 'en-US',
  },
  {
    id: 'model-prices-tablet-zh-dark',
    scenario: 'model-prices-mixed',
    path: '/settings/model-prices',
    viewport: visualViewports[2],
    theme: 'dark',
    locale: 'zh-CN',
  },
  {
    id: 'settings-dirty-desktop-en-light',
    scenario: 'settings-dirty',
    path: '/settings',
    viewport: visualViewports[3],
    theme: 'light',
    locale: 'en-US',
  },
  {
    id: 'settings-validation-mobile-zh-light',
    scenario: 'settings-validation',
    path: '/settings',
    viewport: visualViewports[0],
    theme: 'light',
    locale: 'zh-CN',
  },
  {
    id: 'usage-quality-desktop-en-dark',
    scenario: 'usage-quality',
    path: '/monitor?tab=usage&range=24h',
    viewport: visualViewports[3],
    theme: 'dark',
    locale: 'en-US',
  },
  {
    id: 'usage-quality-tablet-zh-light',
    scenario: 'usage-quality',
    path: '/monitor?tab=usage&range=24h',
    viewport: visualViewports[1],
    theme: 'light',
    locale: 'zh-CN',
  },
  {
    id: 'logs-signal-tablet-en-light',
    scenario: 'logs-signal-path',
    path: '/monitor?tab=logs',
    viewport: visualViewports[2],
    theme: 'light',
    locale: 'en-US',
  },
  {
    id: 'logs-signal-mobile-zh-dark',
    scenario: 'logs-signal-path',
    path: '/monitor?tab=logs',
    viewport: visualViewports[0],
    theme: 'dark',
    locale: 'zh-CN',
  },
  {
    id: 'inspector-routing-desktop-en-light',
    scenario: 'inspector-routing',
    path: '/monitor?tab=inspector&protocol=openai-chat-completions&external_model=stable-public-alias&access_key_id=12',
    viewport: visualViewports[3],
    theme: 'light',
    locale: 'en-US',
  },
  {
    id: 'inspector-routing-tablet-zh-dark',
    scenario: 'inspector-routing',
    path: '/monitor?tab=inspector&protocol=openai-chat-completions&external_model=stable-public-alias&access_key_id=12',
    viewport: visualViewports[1],
    theme: 'dark',
    locale: 'zh-CN',
  },
]

export const visualFixtureData = Object.freeze({
  requestId: '5297f18d-3ca7-42d9-8a5b-2ce08ccd1b01',
  accessKeySuffix: 'a11e',
  groupName: 'Visual Fixture OpenAI Group',
  modelName: 'visual-fixture-model-with-a-stable-long-identifier',
  longIdentifier:
    'visual-fixture-client-segment-segment-segment-segment-segment-segment-segment-segment-segment-segment-segment-segment',
  observedAt: visualClock,
})
