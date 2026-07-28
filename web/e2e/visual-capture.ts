import { createHash } from 'node:crypto'
import { mkdir, writeFile } from 'node:fs/promises'
import { relative, resolve, sep } from 'node:path'

import type { Page, TestInfo } from '@playwright/test'

import { visualClock, visualFixtureVersion, type VisualScenarioCase } from './visual-fixtures'

interface Rect {
  x: number
  y: number
  width: number
  height: number
}

interface VisualGeometry {
  viewport: { width: number; height: number }
  document: { width: number; height: number }
  scroll: { x: number; y: number }
  main: Rect
  heading: Rect
  horizontalOverflow: number
}

export interface VisualCapture {
  id: string
  scenario: VisualScenarioCase['scenario']
  path: string
  locale: VisualScenarioCase['locale']
  theme: VisualScenarioCase['theme']
  viewport: VisualScenarioCase['viewport']
  screenshot: {
    path: string
    sha256: string
  }
  geometry: VisualGeometry
}

function normalizePath(value: string): string {
  return value.split(sep).join('/')
}

function artifactRoot(): string {
  return resolve(process.cwd(), process.env.GPT_LOAD_E2E_ARTIFACT_DIR ?? 'test-results/unscoped')
}

export async function captureVisualScenario(
  page: Page,
  testInfo: TestInfo,
  scenarioCase: VisualScenarioCase,
): Promise<VisualCapture> {
  const geometry = await page.evaluate(() => {
    const main = document.querySelector('main')
    const heading = main?.querySelector('h1')
    if (!(main instanceof HTMLElement) || !(heading instanceof HTMLElement)) {
      throw new Error('Visual scenario requires one rendered main heading')
    }
    const mainRect = main.getBoundingClientRect()
    const headingRect = heading.getBoundingClientRect()
    const round = (value: number) => Math.round(value * 1_000) / 1_000
    const rect = (value: DOMRect): Rect => ({
      x: round(value.x),
      y: round(value.y),
      width: round(value.width),
      height: round(value.height),
    })
    return {
      viewport: {
        width: document.documentElement.clientWidth,
        height: document.documentElement.clientHeight,
      },
      document: {
        width: document.documentElement.scrollWidth,
        height: document.documentElement.scrollHeight,
      },
      scroll: { x: window.scrollX, y: window.scrollY },
      main: rect(mainRect),
      heading: rect(headingRect),
      horizontalOverflow: Math.max(
        0,
        document.documentElement.scrollWidth - document.documentElement.clientWidth,
      ),
    }
  })

  if (
    geometry.viewport.width !== scenarioCase.viewport.width ||
    geometry.viewport.height !== scenarioCase.viewport.height
  ) {
    throw new Error(`Visual viewport drifted for ${scenarioCase.id}`)
  }
  if (geometry.horizontalOverflow !== 0) {
    throw new Error(`Body overflowed horizontally for ${scenarioCase.id}`)
  }
  if (geometry.scroll.x !== 0 || geometry.scroll.y !== 0) {
    throw new Error(`Visual capture did not start at the document origin for ${scenarioCase.id}`)
  }
  if (
    geometry.heading.width <= 0 ||
    geometry.heading.height <= 0 ||
    geometry.heading.x < 0 ||
    geometry.heading.x + geometry.heading.width > geometry.viewport.width
  ) {
    throw new Error(`Main heading geometry is invalid for ${scenarioCase.id}`)
  }

  const screenshotPath = testInfo.outputPath('candidate.png')
  const screenshot = await page.screenshot({
    path: screenshotPath,
    fullPage: true,
    animations: 'disabled',
    caret: 'hide',
  })
  return {
    id: scenarioCase.id,
    scenario: scenarioCase.scenario,
    path: scenarioCase.path,
    locale: scenarioCase.locale,
    theme: scenarioCase.theme,
    viewport: scenarioCase.viewport,
    screenshot: {
      path: normalizePath(relative(artifactRoot(), screenshotPath)),
      sha256: createHash('sha256').update(screenshot).digest('hex'),
    },
    geometry,
  }
}

export async function writeVisualScenarioManifest(captures: VisualCapture[]): Promise<void> {
  const sortedCaptures = [...captures].sort((left, right) => left.id.localeCompare(right.id))
  const payload = {
    schema_version: 1,
    fixture_version: visualFixtureVersion,
    clock: visualClock,
    captures: sortedCaptures,
  }
  const manifest = {
    ...payload,
    manifest_sha256: createHash('sha256').update(JSON.stringify(payload)).digest('hex'),
  }
  const root = artifactRoot()
  await mkdir(root, { recursive: true })
  await writeFile(
    resolve(root, 'scenario-manifest.json'),
    `${JSON.stringify(manifest, null, 2)}\n`,
    'utf8',
  )
}
