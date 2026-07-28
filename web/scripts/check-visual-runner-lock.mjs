import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const approvedRenderFingerprint = {
  viewports: [
    { width: 375, height: 812 },
    { width: 768, height: 900 },
    { width: 1024, height: 900 },
    { width: 1440, height: 900 },
  ],
  device_scale_factor: 1,
  locales: ['en-US', 'zh-CN'],
  timezone: 'UTC',
  color_schemes: ['light', 'dark'],
  reduced_motion: 'reduce',
  animations_disabled: true,
}

function fail(message) {
  throw new Error(`visual runner lock: ${message}`)
}

function fullDigest(value) {
  return typeof value === 'string' && /^sha256:[0-9a-f]{64}$/.test(value)
}

export function validateVisualRunnerLock(lock) {
  if (!lock || typeof lock !== 'object') fail('manifest must be an object')
  if (lock.schema_version !== 2) fail('unsupported schema version')
  if (
    typeof lock.container?.image !== 'string' ||
    !/@sha256:[0-9a-f]{64}$/.test(lock.container.image)
  ) {
    fail('container image must use a full sha256 manifest digest')
  }
  if (/:(latest|main|master)(?:@|$)/.test(lock.container.image)) {
    fail('mutable container tag is forbidden')
  }
  for (const platform of ['linux/amd64', 'linux/arm64']) {
    if (!fullDigest(lock.container.platform_digests?.[platform])) {
      fail(`missing full ${platform} image digest`)
    }
  }

  for (const field of [
    'container_node',
    'package_manager',
    'playwright',
    'vite',
    'zlib',
    'brotli',
  ]) {
    if (typeof lock.runtime?.[field] !== 'string' || lock.runtime[field].length === 0) {
      fail(`missing runtime ${field}`)
    }
  }
  for (const browser of ['chromium', 'webkit']) {
    if (
      typeof lock.browsers?.[browser]?.revision !== 'string' ||
      typeof lock.browsers?.[browser]?.version !== 'string' ||
      lock.browsers[browser].revision.length === 0 ||
      lock.browsers[browser].version.length === 0
    ) {
      fail(`incomplete ${browser} fingerprint`)
    }
  }
  if (
    !Number.isSafeInteger(lock.fonts?.count) ||
    lock.fonts.count <= 0 ||
    !/^[0-9a-f]{64}$/.test(lock.fonts?.sha256 ?? '')
  ) {
    fail('incomplete font inventory')
  }
  if (JSON.stringify(lock.render_fingerprint) !== JSON.stringify(approvedRenderFingerprint)) {
    fail('render fingerprint does not match the approved matrix')
  }
  if (lock.pixel_baseline?.enabled !== false || !lock.pixel_baseline.external_gate) {
    fail('pixel baseline must remain disabled until human approval is recorded')
  }
  return lock
}

export async function loadVisualRunnerContract() {
  const lockPath = join(webRoot, 'visual-runner.lock.json')
  const packagePath = join(webRoot, 'package.json')
  const lockSource = await readFile(lockPath, 'utf8')
  const lock = validateVisualRunnerLock(JSON.parse(lockSource))
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
  const browserManifest = JSON.parse(await readFile(browserPath, 'utf8'))
  const installedBrowsers = Object.fromEntries(
    ['chromium', 'webkit'].map((name) => {
      const browser = browserManifest.browsers.find((candidate) => candidate.name === name)
      if (!browser) fail(`Playwright package is missing ${name}`)
      return [
        name,
        {
          revision: browser.revision,
          version: browser.browserVersion,
        },
      ]
    }),
  )
  const packageRuntime = {
    package_manager: packageJSON.packageManager,
    playwright: packageJSON.devDependencies['@playwright/test'],
    vite: packageJSON.devDependencies.vite,
  }
  for (const [field, value] of Object.entries(packageRuntime)) {
    if (lock.runtime[field] !== value) {
      fail(`${field} mismatch: expected ${lock.runtime[field]}, received ${value}`)
    }
  }
  if (JSON.stringify(installedBrowsers) !== JSON.stringify(lock.browsers)) {
    fail(`browser package fingerprint mismatch: ${JSON.stringify(installedBrowsers)}`)
  }
  return {
    lock,
    lockSource,
    lockSha256: createHash('sha256').update(lockSource).digest('hex'),
  }
}

async function runManifestCheck() {
  if (process.argv[2] !== '--manifest-only') {
    fail('use --manifest-only for the host-side contract check')
  }
  const contract = await loadVisualRunnerContract()
  process.stdout.write(
    `${JSON.stringify({
      check: 'manifest-only',
      runner_execution_verified: false,
      lock_sha256: contract.lockSha256,
      container: contract.lock.container,
      runtime: contract.lock.runtime,
      browsers: contract.lock.browsers,
      fonts: contract.lock.fonts,
      render_fingerprint: contract.lock.render_fingerprint,
      pixel_baseline: contract.lock.pixel_baseline,
    })}\n`,
  )
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await runManifestCheck()
}
