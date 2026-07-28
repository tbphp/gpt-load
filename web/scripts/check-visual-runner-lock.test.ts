import { spawnSync } from 'node:child_process'
import { resolve } from 'node:path'

describe('visual runner manifest validation', () => {
  const script = resolve(process.cwd(), 'scripts/check-visual-runner-lock.mjs')

  it('labels the local/host check as manifest-only and leaves runner execution external', () => {
    const result = spawnSync(process.execPath, [script, '--manifest-only'], {
      cwd: process.cwd(),
      encoding: 'utf8',
    })

    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toMatchObject({
      check: 'manifest-only',
      runner_execution_verified: false,
      pixel_baseline: {
        enabled: false,
        external_gate: expect.stringContaining('Phase 5'),
      },
    })
  })

  it('refuses to masquerade a host check as an executed visual runner gate', () => {
    const result = spawnSync(process.execPath, [script], {
      cwd: process.cwd(),
      encoding: 'utf8',
    })

    expect(result.status).not.toBe(0)
    expect(result.stderr).toContain('validates the manifest only')
    expect(result.stderr).toContain('Phase 5 runner/pixel gate is external')
  })
})
