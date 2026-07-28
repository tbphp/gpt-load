import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const lockPath = join(webRoot, 'visual-runner.lock.json')
const packagePath = join(webRoot, 'package.json')
const mode = process.argv[2]
if (mode !== '--manifest-only') {
  throw new Error(
    'visual runner lock: this script validates the manifest only; the Phase 5 runner/pixel gate is external',
  )
}
const lockSource = await readFile(lockPath, 'utf8')
const lock = JSON.parse(lockSource)
const packageJSON = JSON.parse(await readFile(packagePath, 'utf8'))
const browserPath = join(
  webRoot,
  'node_modules',
  '.pnpm',
  `playwright-core@${packageJSON.devDependencies['@playwright/test']}`,
  'node_modules',
  'playwright-core',
  'browsers.json',
)
const browsers = JSON.parse(await readFile(browserPath, 'utf8'))
const chromium = browsers.browsers.find(({ name }) => name === 'chromium')

function fail(message) {
  throw new Error(`visual runner lock: ${message}`)
}

if (lock.schema_version !== 1) fail('unsupported schema version')
if (
  typeof lock.container?.image !== 'string' ||
  !/@sha256:[0-9a-f]{64}$/.test(lock.container.image)
) {
  fail('container image must use a full sha256 digest')
}
if (/:(latest|main|master)(?:@|$)/.test(lock.container.image)) {
  fail('mutable container tag is forbidden')
}
for (const platform of ['linux/amd64', 'linux/arm64']) {
  if (!/^sha256:[0-9a-f]{64}$/.test(lock.container.platform_digests?.[platform] ?? '')) {
    fail(`missing full ${platform} image digest`)
  }
}

const actualRuntime = {
  node: process.versions.node,
  playwright: packageJSON.devDependencies['@playwright/test'],
  vite: packageJSON.devDependencies.vite,
  zlib: process.versions.zlib,
  brotli: process.versions.brotli,
}
if (JSON.stringify(actualRuntime) !== JSON.stringify(lock.runtime)) {
  fail(`runtime fingerprint mismatch: ${JSON.stringify(actualRuntime)}`)
}

const requiredRenderFingerprint = {
  browser: 'chromium',
  browser_revision: chromium?.revision,
  browser_version: chromium?.browserVersion,
  viewport: { width: 1440, height: 900 },
  device_scale_factor: 1,
  locale: 'en-US',
  timezone: 'UTC',
  color_scheme: 'light',
  reduced_motion: 'reduce',
  animations_disabled: true,
}
if (
  !chromium ||
  JSON.stringify(requiredRenderFingerprint) !== JSON.stringify(lock.render_fingerprint)
) {
  fail(`render fingerprint mismatch: ${JSON.stringify(requiredRenderFingerprint)}`)
}

if (lock.pixel_baseline?.enabled !== false || !lock.pixel_baseline.external_gate) {
  fail('pixel baseline must remain disabled until the external gate is recorded')
}

process.stdout.write(
  `${JSON.stringify({
    check: 'manifest-only',
    runner_execution_verified: false,
    lock_sha256: createHash('sha256').update(lockSource).digest('hex'),
    runtime: actualRuntime,
    render_fingerprint: requiredRenderFingerprint,
    pixel_baseline: lock.pixel_baseline,
  })}\n`,
)
