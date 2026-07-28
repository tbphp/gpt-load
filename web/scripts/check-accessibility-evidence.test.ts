import { validateAccessibilityEvidence } from './check-accessibility-evidence.mjs'

const sourceSHA = 'a'.repeat(40)
const artifactSHA = 'b'.repeat(64)

function row(id: string, status: 'PASS' | 'NOT RUN' | 'EXTERNAL') {
  return {
    id,
    mode: id.startsWith('linux-') ? 'automated' : 'manual-geometry-functional',
    status,
    operator: status === 'PASS' ? 'Codex automated runner' : 'NOT RUN',
    source_sha: sourceSHA,
    executed_at: status === 'PASS' ? '2026-07-28T21:19:41Z' : null,
    environment: {
      os_name: id.startsWith('linux-') ? 'Ubuntu' : 'macOS',
      os_version: id.startsWith('linux-') ? '24.04' : '26.6',
      architecture: 'arm64',
    },
    browser: {
      name: id.endsWith('webkit') ? 'WebKit' : 'Chromium',
      version: '1.0',
    },
    assistive_technology: { name: 'none', version: 'not applicable' },
    scenarios: ['keyboard and semantic flow'],
    artifacts:
      status === 'PASS' ? [{ path: 'evidence/artifacts/example.json', sha256: artifactSHA }] : [],
    notes: status === 'PASS' ? 'Automated assertions only.' : 'No human execution evidence.',
  }
}

function validMatrix() {
  return {
    schema_version: 1,
    source_sha: sourceSHA,
    generated_at: '2026-07-28T21:19:41Z',
    rows: [
      row('linux-chromium-automated', 'PASS'),
      row('linux-webkit-automated', 'PASS'),
      row('macos-chrome-manual', 'NOT RUN'),
      row('macos-safari-manual', 'NOT RUN'),
      {
        ...row('safari-voiceover-manual', 'NOT RUN'),
        mode: 'manual-at',
        assistive_technology: { name: 'VoiceOver', version: 'macOS 26.6 built-in' },
      },
      {
        ...row('windows-chrome-nvda-external', 'EXTERNAL'),
        mode: 'external-at',
        environment: { os_name: 'Windows', os_version: 'UNAVAILABLE', architecture: 'UNAVAILABLE' },
        assistive_technology: { name: 'NVDA', version: 'UNAVAILABLE' },
      },
    ],
  }
}

describe('accessibility evidence schema', () => {
  it('accepts the approved six-row matrix', () => {
    expect(validateAccessibilityEvidence(validMatrix())).toEqual(validMatrix())
  })

  it.each([
    [
      'blank source SHA',
      (matrix: ReturnType<typeof validMatrix>) => {
        matrix.source_sha = ''
      },
    ],
    [
      'missing environment version',
      (matrix: ReturnType<typeof validMatrix>) => {
        matrix.rows[0]!.environment.os_version = ''
      },
    ],
    [
      'PASS without artifacts',
      (matrix: ReturnType<typeof validMatrix>) => {
        matrix.rows[0]!.artifacts = []
      },
    ],
    [
      'automated PASS for a human AT row',
      (matrix: ReturnType<typeof validMatrix>) => {
        const voiceOver = matrix.rows[4]!
        voiceOver.status = 'PASS'
        voiceOver.operator = 'Codex automated runner'
        voiceOver.executed_at = '2026-07-28T21:19:41Z'
        voiceOver.artifacts = [{ path: 'evidence/artifacts/example.json', sha256: artifactSHA }]
      },
    ],
  ])('rejects %s', (_label, mutate) => {
    const matrix = validMatrix()
    mutate(matrix)
    expect(() => validateAccessibilityEvidence(matrix)).toThrow()
  })
})
