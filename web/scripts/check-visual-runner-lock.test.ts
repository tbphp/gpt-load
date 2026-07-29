import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { validateVisualRunnerLock } from './check-visual-runner-lock.mjs'
import { parseVisualRunnerInvocation } from './run-visual-runner.mjs'

describe('visual runner contract', () => {
  const checker = resolve(process.cwd(), 'scripts/check-visual-runner-lock.mjs')
  const runner = resolve(process.cwd(), 'scripts/run-visual-runner.mjs')
  const lock = JSON.parse(readFileSync(resolve('visual-runner.lock.json'), 'utf8'))

  it('pins both browser engines, the font inventory, and the complete render matrix', () => {
    const result = spawnSync(process.execPath, [checker, '--manifest-only'], {
      cwd: process.cwd(),
      encoding: 'utf8',
    })

    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toMatchObject({
      check: 'manifest-only',
      runner_execution_verified: false,
      container: {
        platform_digests: {
          'linux/amd64': expect.stringMatching(/^sha256:[0-9a-f]{64}$/),
          'linux/arm64': expect.stringMatching(/^sha256:[0-9a-f]{64}$/),
        },
      },
      browsers: {
        chromium: { revision: '1234', version: '151.0.7922.34' },
        webkit: { revision: '2336', version: '26.5' },
      },
      fonts: {
        count: 50,
        sha256: 'b8ef8574b460985896b67131880009351c299e3bdbe3346a77b16650714d6365',
      },
      render_fingerprint: {
        viewports: [
          { width: 375, height: 812 },
          { width: 768, height: 900 },
          { width: 1024, height: 900 },
          { width: 1440, height: 900 },
        ],
        locales: ['en-US', 'zh-CN'],
        color_schemes: ['light', 'dark'],
        timezone: 'UTC',
        device_scale_factor: 1,
        reduced_motion: 'reduce',
        animations_disabled: true,
      },
    })
  })

  it.each([
    [
      'mutable image',
      (candidate: typeof lock) => {
        candidate.container.image = 'mcr.microsoft.com/playwright:latest'
      },
    ],
    [
      'missing architecture digest',
      (candidate: typeof lock) => {
        delete candidate.container.platform_digests['linux/arm64']
      },
    ],
    [
      'incomplete browser fingerprint',
      (candidate: typeof lock) => {
        delete candidate.browsers.webkit.version
      },
    ],
    [
      'incomplete font inventory',
      (candidate: typeof lock) => {
        candidate.fonts.sha256 = ''
      },
    ],
  ])('rejects %s', (_label, mutate) => {
    const candidate = structuredClone(lock)
    mutate(candidate)
    expect(() => validateVisualRunnerLock(candidate)).toThrow()
  })

  it('describes a pinned container command without executing or falling back to the host', () => {
    const result = spawnSync(process.execPath, [runner, '--dry-run'], {
      cwd: process.cwd(),
      encoding: 'utf8',
    })

    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toMatchObject({
      mode: 'dry-run',
      host_fallback: false,
      container: {
        image: expect.stringMatching(/@sha256:[0-9a-f]{64}$/),
        platform: expect.stringMatching(/^linux\/(amd64|arm64)$/),
        platform_digest: expect.stringMatching(/^sha256:[0-9a-f]{64}$/),
      },
      command: expect.arrayContaining(['docker', 'run', '--rm']),
    })
  })

  it('accepts only the canonical Chromium candidate invocation', () => {
    expect(parseVisualRunnerInvocation(['--candidate', '--browser=chromium'])).toEqual({
      mode: 'candidate',
      browser: 'chromium',
    })
    expect(() => parseVisualRunnerInvocation(['--candidate', '--browser=webkit'])).toThrowError(
      /Chromium/,
    )
    expect(readFileSync(runner, 'utf8')).not.toContain(
      'candidate execution requires the deterministic Phase 5 scenario suite',
    )
    expect(readFileSync(runner, 'utf8')).toContain(
      'corepack install --global ${contract.lock.runtime.package_manager}',
    )
  })

  it('requires an explicit pinned browser for functional execution', () => {
    expect(parseVisualRunnerInvocation(['--functional', '--browser=webkit'])).toEqual({
      mode: 'functional',
      browser: 'webkit',
    })
    expect(() => parseVisualRunnerInvocation(['--functional'])).toThrowError(/explicit browser/)
    expect(readFileSync(runner, 'utf8')).not.toContain(
      'functional execution requires the Phase 5 cross-browser flow suite',
    )
    expect(readFileSync(runner, 'utf8')).toContain('e2e/accessibility.spec.ts')
    expect(readFileSync(runner, 'utf8')).toContain('GPT_LOAD_E2E_SOURCE_SHA')
  })
})
