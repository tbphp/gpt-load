import { createHash } from 'node:crypto'
import { execFile } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { promisify } from 'node:util'
import { fileURLToPath } from 'node:url'

import { loadVisualRunnerContract } from './check-visual-runner-lock.mjs'

const executeFile = promisify(execFile)
const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const repositoryRoot = resolve(webRoot, '..')

function fail(message) {
  throw new Error(`visual runner: ${message}`)
}

function selectedPlatform() {
  if (process.arch === 'arm64') return 'linux/arm64'
  if (process.arch === 'x64') return 'linux/amd64'
  fail(`unsupported host architecture ${process.arch}`)
}

function parseMode(arguments_) {
  const modes = ['--dry-run', '--verify', '--candidate', '--functional'].filter((mode) =>
    arguments_.includes(mode),
  )
  if (modes.length !== 1) {
    fail('choose exactly one of --dry-run, --verify, --candidate, or --functional')
  }
  return modes[0].slice(2)
}

function fingerprintScript(lock) {
  return String.raw`
const { createHash } = require('node:crypto')
const { execFileSync } = require('node:child_process')
const { existsSync } = require('node:fs')
const fonts = execFileSync('bash', ['-lc',
  'fc-list --format "%{file}|%{family}|%{style}\\n" | LC_ALL=C sort -u'
])
const fontLines = fonts.toString('utf8').trimEnd().split('\n').filter(Boolean)
const result = {
  runtime: {
    container_node: process.versions.node,
    zlib: process.versions.zlib,
    brotli: process.versions.brotli,
    platform: process.platform,
    architecture: process.arch,
  },
  browsers: {
    chromium: {
      revision: ${JSON.stringify(lock.browsers.chromium.revision)},
      present: existsSync(${JSON.stringify(
        `/ms-playwright/chromium-${lock.browsers.chromium.revision}`,
      )}),
    },
    webkit: {
      revision: ${JSON.stringify(lock.browsers.webkit.revision)},
      present: existsSync(${JSON.stringify(`/ms-playwright/webkit-${lock.browsers.webkit.revision}`)}),
    },
  },
  fonts: {
    count: fontLines.length,
    sha256: createHash('sha256').update(fonts).digest('hex'),
  },
}
process.stdout.write(JSON.stringify(result))
`
}

function findPlatformDigest(rawManifest, platform) {
  const [os, architecture] = platform.split('/')
  const manifest = JSON.parse(rawManifest)
  const match = manifest.manifests?.find(
    (candidate) =>
      candidate.platform?.os === os && candidate.platform?.architecture === architecture,
  )
  if (!match?.digest) fail(`image manifest is missing ${platform}`)
  return match.digest
}

async function verifyContainer(contract, platform) {
  const imageManifest = await executeFile(
    'docker',
    ['buildx', 'imagetools', 'inspect', '--raw', contract.lock.container.image],
    { maxBuffer: 4 * 1024 * 1024 },
  )
  const actualPlatformDigest = findPlatformDigest(imageManifest.stdout, platform)
  const expectedPlatformDigest = contract.lock.container.platform_digests[platform]
  if (actualPlatformDigest !== expectedPlatformDigest) {
    fail(
      `${platform} digest mismatch: expected ${expectedPlatformDigest}, received ${actualPlatformDigest}`,
    )
  }

  const command = [
    'docker',
    'run',
    '--rm',
    '--platform',
    platform,
    contract.lock.container.image,
    'node',
    '-e',
    fingerprintScript(contract.lock),
  ]
  const fingerprintResult = await executeFile(command[0], command.slice(1), {
    maxBuffer: 4 * 1024 * 1024,
  })
  const fingerprint = JSON.parse(fingerprintResult.stdout)
  const expectedArchitecture = platform.endsWith('/arm64') ? 'arm64' : 'x64'
  const expectedRuntime = {
    container_node: contract.lock.runtime.container_node,
    zlib: contract.lock.runtime.zlib,
    brotli: contract.lock.runtime.brotli,
    platform: 'linux',
    architecture: expectedArchitecture,
  }
  if (JSON.stringify(fingerprint.runtime) !== JSON.stringify(expectedRuntime)) {
    fail(`container runtime mismatch: ${JSON.stringify(fingerprint.runtime)}`)
  }
  for (const browser of ['chromium', 'webkit']) {
    if (
      fingerprint.browsers[browser].revision !== contract.lock.browsers[browser].revision ||
      fingerprint.browsers[browser].present !== true
    ) {
      fail(`${browser} binary fingerprint mismatch`)
    }
  }
  if (JSON.stringify(fingerprint.fonts) !== JSON.stringify(contract.lock.fonts)) {
    fail(`font inventory mismatch: ${JSON.stringify(fingerprint.fonts)}`)
  }
  return { command, fingerprint, actualPlatformDigest }
}

async function gitValue(arguments_) {
  const result = await executeFile('git', arguments_, { cwd: repositoryRoot })
  return result.stdout.trim()
}

async function writeEvidence(evidence) {
  const path = join(webRoot, 'test-results', 'visual-runner.json')
  await mkdir(dirname(path), { recursive: true })
  await writeFile(path, `${JSON.stringify(evidence, null, 2)}\n`)
  return path
}

const contract = await loadVisualRunnerContract()
const platform = selectedPlatform()
const mode = parseMode(process.argv.slice(2))
const platformDigest = contract.lock.container.platform_digests[platform]
const containerCommand = [
  'docker',
  'run',
  '--rm',
  '--platform',
  platform,
  contract.lock.container.image,
]

if (mode === 'dry-run') {
  process.stdout.write(
    `${JSON.stringify({
      mode,
      host_fallback: false,
      container: {
        image: contract.lock.container.image,
        platform,
        platform_digest: platformDigest,
      },
      command: containerCommand,
    })}\n`,
  )
  process.exit(0)
}

if (mode === 'candidate' || mode === 'functional') {
  fail(`${mode} execution requires the deterministic Phase 5 scenario suite`)
}

const verification = await verifyContainer(contract, platform)
const evidence = {
  schema_version: 1,
  mode,
  source_sha: await gitValue(['rev-parse', 'HEAD']),
  source_dirty: (await gitValue(['status', '--porcelain'])).length > 0,
  lock_sha256: contract.lockSha256,
  image: contract.lock.container.image,
  platform,
  platform_digest: verification.actualPlatformDigest,
  host_fallback: false,
  runtime: verification.fingerprint.runtime,
  browsers: contract.lock.browsers,
  fonts: verification.fingerprint.fonts,
  render_fingerprint: contract.lock.render_fingerprint,
  evidence_sha256: '',
}
evidence.evidence_sha256 = createHash('sha256').update(JSON.stringify(evidence)).digest('hex')
const evidencePath = await writeEvidence(evidence)
process.stdout.write(
  `${JSON.stringify({
    mode,
    runner_execution_verified: true,
    evidence: evidencePath,
    source_sha: evidence.source_sha,
    source_dirty: evidence.source_dirty,
    platform,
    platform_digest: verification.actualPlatformDigest,
  })}\n`,
)
