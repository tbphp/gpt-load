import { createHash } from 'node:crypto'
import { execFile } from 'node:child_process'
import { access, cp, mkdir, mkdtemp, readFile, rename, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { basename, dirname, join, resolve } from 'node:path'
import { promisify } from 'node:util'
import { fileURLToPath } from 'node:url'

import { loadVisualRunnerContract } from './check-visual-runner-lock.mjs'

const executeFile = promisify(execFile)
const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const repositoryRoot = resolve(webRoot, '..')
const runnerTemporaryPrefix = 'gpt-load-visual-runner-'

function fail(message) {
  throw new Error(`visual runner: ${message}`)
}

function selectedPlatform() {
  if (process.arch === 'arm64') return 'linux/arm64'
  if (process.arch === 'x64') return 'linux/amd64'
  fail(`unsupported host architecture ${process.arch}`)
}

export function parseVisualRunnerInvocation(arguments_) {
  const normalizedArguments = arguments_.filter((argument) => argument !== '--')
  const supportedModes = ['dry-run', 'verify', 'candidate', 'functional']
  const modes = normalizedArguments
    .filter((argument) => argument.startsWith('--') && !argument.startsWith('--browser='))
    .map((argument) => argument.slice(2))
    .filter((argument) => supportedModes.includes(argument))
  const unknown = normalizedArguments.filter(
    (argument) =>
      !supportedModes.some((mode) => argument === `--${mode}`) &&
      !argument.startsWith('--browser='),
  )
  if (unknown.length > 0) fail(`unknown option ${unknown[0]}`)
  if (modes.length !== 1) {
    fail('choose exactly one of --dry-run, --verify, --candidate, or --functional')
  }

  const browserArguments = normalizedArguments
    .filter((argument) => argument.startsWith('--browser='))
    .map((argument) => argument.slice('--browser='.length))
  if (browserArguments.length > 1) fail('choose at most one browser')
  const browser = browserArguments[0]
  if (browser !== undefined && browser !== 'chromium' && browser !== 'webkit') {
    fail(`unsupported browser ${browser}`)
  }
  if (modes[0] === 'candidate' && browser !== 'chromium') {
    fail('candidate capture requires Chromium')
  }
  if (modes[0] === 'functional' && browser === undefined) {
    fail('functional execution requires an explicit browser')
  }
  if (modes[0] === 'verify' && browser !== undefined) {
    fail('verify does not accept a browser')
  }
  return {
    mode: modes[0],
    ...(browser === undefined ? {} : { browser }),
  }
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

async function runFile(command, arguments_, options = {}) {
  return executeFile(command, arguments_, {
    maxBuffer: 64 * 1024 * 1024,
    ...options,
  })
}

async function verifyContainer(contract, platform) {
  const imageManifest = await runFile(
    'docker',
    ['buildx', 'imagetools', 'inspect', '--raw', contract.lock.container.image],
    { encoding: 'utf8' },
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
  const fingerprintResult = await runFile(command[0], command.slice(1), { encoding: 'utf8' })
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
  const result = await runFile('git', arguments_, { cwd: repositoryRoot, encoding: 'utf8' })
  return result.stdout.trim()
}

async function writeRunnerEvidence(evidence) {
  const path = join(webRoot, 'test-results', 'visual-runner.json')
  await mkdir(dirname(path), { recursive: true })
  await writeFile(path, `${JSON.stringify(evidence, null, 2)}\n`)
  return path
}

function sha256(value) {
  return createHash('sha256').update(value).digest('hex')
}

async function createSourceStage() {
  const temporaryRoot = await mkdtemp(join(tmpdir(), runnerTemporaryPrefix))
  const archivePath = join(temporaryRoot, 'source.tar')
  const workspace = join(temporaryRoot, 'workspace')
  await mkdir(workspace)
  const archive = await runFile('git', ['archive', '--format=tar', 'HEAD'], {
    cwd: repositoryRoot,
    encoding: 'buffer',
  })
  await writeFile(archivePath, archive.stdout)
  await runFile('tar', ['-xf', archivePath, '-C', workspace])
  return { temporaryRoot, workspace }
}

async function buildLinuxApplication(workspace, platform) {
  await runFile('corepack', ['pnpm', '--dir', webRoot, 'run', 'build'], {
    cwd: repositoryRoot,
    encoding: 'utf8',
  })
  const architecture = platform.endsWith('/arm64') ? 'arm64' : 'amd64'
  await runFile('go', ['build', '-trimpath', '-o', join(workspace, 'gpt-load'), '.'], {
    cwd: repositoryRoot,
    encoding: 'utf8',
    env: {
      ...process.env,
      CGO_ENABLED: '0',
      GOOS: 'linux',
      GOARCH: architecture,
    },
  })
}

async function runContainerSuite(contract, platform, workspace, browser, spec) {
  const command = [
    'docker',
    'run',
    '--rm',
    '--platform',
    platform,
    '--env',
    'CI=1',
    '--env',
    'PLAYWRIGHT_BROWSERS_PATH=/ms-playwright',
    '--env',
    'GPT_LOAD_E2E_APP_PORT=43101',
    '--volume',
    `${workspace}:/workspace`,
    '--workdir',
    '/workspace',
    contract.lock.container.image,
    'bash',
    '-lc',
    [
      'corepack pnpm --dir /workspace/web install --frozen-lockfile',
      `corepack pnpm --dir /workspace/web run test:e2e ${spec} --project=${browser}`,
    ].join(' && '),
  ]
  const result = await runFile(command[0], command.slice(1), { encoding: 'utf8' })
  return { command, stdout: result.stdout, stderr: result.stderr }
}

async function validateScenarioArtifacts(artifactRoot) {
  const manifestPath = join(artifactRoot, 'scenario-manifest.json')
  const manifestSource = await readFile(manifestPath, 'utf8')
  const manifest = JSON.parse(manifestSource)
  const { manifest_sha256: declaredHash, ...payload } = manifest
  if (!/^[0-9a-f]{64}$/.test(declaredHash) || sha256(JSON.stringify(payload)) !== declaredHash) {
    fail('scenario manifest hash is invalid')
  }
  if (!Array.isArray(manifest.captures) || manifest.captures.length === 0) {
    fail('scenario manifest has no captures')
  }
  const ids = new Set()
  for (const capture of manifest.captures) {
    if (typeof capture.id !== 'string' || ids.has(capture.id)) {
      fail('scenario manifest contains a duplicate or invalid ID')
    }
    ids.add(capture.id)
    const screenshotPath = resolve(artifactRoot, capture.screenshot?.path ?? '')
    if (!screenshotPath.startsWith(`${artifactRoot}/`)) fail('scenario artifact escaped its root')
    const screenshot = await readFile(screenshotPath)
    if (sha256(screenshot) !== capture.screenshot.sha256) {
      fail(`screenshot hash mismatch for ${capture.id}`)
    }
  }
  return {
    manifest,
    manifestPath,
    manifestFileSha256: sha256(manifestSource),
  }
}

async function publishCandidate({
  contract,
  platform,
  verification,
  sourceSHA,
  artifactRoot,
  scenarioArtifacts,
}) {
  const candidatesRoot = join(webRoot, 'visual-baselines', 'candidates')
  const target = join(candidatesRoot, sourceSHA)
  await access(target).then(
    () => fail(`candidate ${sourceSHA} already exists; automatic replacement is forbidden`),
    () => undefined,
  )
  await mkdir(candidatesRoot, { recursive: true })
  const pending = join(candidatesRoot, `.pending-${sourceSHA}`)
  await mkdir(pending)
  try {
    await cp(artifactRoot, join(pending, 'artifacts'), { recursive: true })
    const payload = {
      schema_version: 1,
      status: 'CANDIDATE',
      source_sha: sourceSHA,
      source_dirty: false,
      lock_sha256: contract.lockSha256,
      image: contract.lock.container.image,
      platform,
      platform_digest: verification.actualPlatformDigest,
      runtime: verification.fingerprint.runtime,
      browsers: contract.lock.browsers,
      fonts: verification.fingerprint.fonts,
      render_fingerprint: contract.lock.render_fingerprint,
      scenario_manifest: {
        path: 'artifacts/scenario-manifest.json',
        sha256: scenarioArtifacts.manifestFileSha256,
        declared_sha256: scenarioArtifacts.manifest.manifest_sha256,
        capture_count: scenarioArtifacts.manifest.captures.length,
      },
      artifacts: scenarioArtifacts.manifest.captures.map((capture) => ({
        id: capture.id,
        path: `artifacts/${capture.screenshot.path}`,
        sha256: capture.screenshot.sha256,
      })),
      human_approval: {
        status: 'NOT RUN',
        activated: false,
        required: true,
      },
    }
    const metadata = {
      ...payload,
      candidate_sha256: sha256(JSON.stringify(payload)),
    }
    await writeFile(join(pending, 'candidate.json'), `${JSON.stringify(metadata, null, 2)}\n`)
    await rename(pending, target)
  } catch (error) {
    await rm(pending, { recursive: true, force: true })
    throw error
  }
  return target
}

async function runCandidate(contract, platform, verification) {
  const sourceDirty =
    (await gitValue(['status', '--porcelain', '--untracked-files=all'])).length > 0
  if (sourceDirty) fail('candidate capture requires a clean source tree')
  const sourceSHA = await gitValue(['rev-parse', 'HEAD'])
  const stage = await createSourceStage()
  try {
    await buildLinuxApplication(stage.workspace, platform)
    const execution = await runContainerSuite(
      contract,
      platform,
      stage.workspace,
      'chromium',
      'e2e/visual-scenarios.spec.ts',
    )
    const artifactRoot = join(
      stage.workspace,
      'web',
      'test-results',
      'chromium-serial-visual-scenarios',
    )
    const scenarioArtifacts = await validateScenarioArtifacts(artifactRoot)
    const target = await publishCandidate({
      contract,
      platform,
      verification,
      sourceSHA,
      artifactRoot,
      scenarioArtifacts,
    })
    return {
      mode: 'candidate',
      browser: 'chromium',
      runner_execution_verified: true,
      source_sha: sourceSHA,
      source_dirty: false,
      platform,
      platform_digest: verification.actualPlatformDigest,
      scenario_manifest_sha256: scenarioArtifacts.manifestFileSha256,
      candidate: target,
      stdout: execution.stdout.trim(),
      stderr: execution.stderr.trim(),
    }
  } finally {
    if (
      dirname(stage.temporaryRoot) !== resolve(tmpdir()) ||
      !basename(stage.temporaryRoot).startsWith(runnerTemporaryPrefix)
    ) {
      fail('refusing to remove an unsafe visual runner directory')
    }
    await rm(stage.temporaryRoot, { recursive: true, force: true })
  }
}

async function main() {
  const contract = await loadVisualRunnerContract()
  const platform = selectedPlatform()
  const invocation = parseVisualRunnerInvocation(process.argv.slice(2))
  const platformDigest = contract.lock.container.platform_digests[platform]
  const containerCommand = [
    'docker',
    'run',
    '--rm',
    '--platform',
    platform,
    contract.lock.container.image,
  ]

  if (invocation.mode === 'dry-run') {
    process.stdout.write(
      `${JSON.stringify({
        ...invocation,
        host_fallback: false,
        container: {
          image: contract.lock.container.image,
          platform,
          platform_digest: platformDigest,
        },
        command: containerCommand,
      })}\n`,
    )
    return
  }

  const verification = await verifyContainer(contract, platform)
  if (invocation.mode === 'candidate') {
    process.stdout.write(
      `${JSON.stringify(await runCandidate(contract, platform, verification))}\n`,
    )
    return
  }
  if (invocation.mode === 'functional') {
    fail('functional execution requires the Phase 5 cross-browser flow suite')
  }

  const evidence = {
    schema_version: 1,
    mode: invocation.mode,
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
  evidence.evidence_sha256 = sha256(JSON.stringify(evidence))
  const evidencePath = await writeRunnerEvidence(evidence)
  process.stdout.write(
    `${JSON.stringify({
      mode: invocation.mode,
      runner_execution_verified: true,
      evidence: evidencePath,
      source_sha: evidence.source_sha,
      source_dirty: evidence.source_dirty,
      platform,
      platform_digest: verification.actualPlatformDigest,
    })}\n`,
  )
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await main()
}
