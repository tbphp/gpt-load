import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const expectedRows = [
  'linux-chromium-automated',
  'linux-webkit-automated',
  'macos-chrome-manual',
  'macos-safari-manual',
  'safari-voiceover-manual',
  'windows-chrome-nvda-external',
]
const allowedStatuses = new Set(['PASS', 'NOT RUN', 'EXTERNAL'])
const allowedModes = new Set([
  'automated',
  'manual-geometry-functional',
  'manual-at',
  'external-at',
])

function assert(condition, message) {
  if (!condition) throw new Error(`accessibility evidence: ${message}`)
}

function nonBlank(value) {
  return typeof value === 'string' && value.trim().length > 0
}

function validSHA(value, length) {
  return typeof value === 'string' && new RegExp(`^[0-9a-f]{${length}}$`).test(value)
}

export function validateAccessibilityEvidence(matrix) {
  assert(matrix && typeof matrix === 'object', 'matrix must be an object')
  assert(matrix.schema_version === 1, 'unsupported schema version')
  assert(validSHA(matrix.source_sha, 40), 'source SHA must be a full commit SHA')
  assert(nonBlank(matrix.generated_at), 'generated_at is required')
  assert(Array.isArray(matrix.rows), 'rows must be an array')
  assert(matrix.rows.length === expectedRows.length, 'matrix must contain the approved six rows')
  assert(
    JSON.stringify(matrix.rows.map((row) => row.id)) === JSON.stringify(expectedRows),
    'matrix rows must use the approved order and identifiers',
  )

  for (const row of matrix.rows) {
    assert(allowedModes.has(row.mode), `${row.id} has an unsupported mode`)
    assert(allowedStatuses.has(row.status), `${row.id} has an unsupported status`)
    assert(row.source_sha === matrix.source_sha, `${row.id} source SHA does not match the matrix`)
    assert(nonBlank(row.operator), `${row.id} operator is required`)
    assert(nonBlank(row.environment?.os_name), `${row.id} OS name is required`)
    assert(nonBlank(row.environment?.os_version), `${row.id} OS version is required`)
    assert(nonBlank(row.environment?.architecture), `${row.id} architecture is required`)
    assert(nonBlank(row.browser?.name), `${row.id} browser name is required`)
    assert(nonBlank(row.browser?.version), `${row.id} browser version is required`)
    assert(
      nonBlank(row.assistive_technology?.name),
      `${row.id} assistive technology name is required`,
    )
    assert(
      nonBlank(row.assistive_technology?.version),
      `${row.id} assistive technology version is required`,
    )
    assert(
      Array.isArray(row.scenarios) && row.scenarios.length > 0 && row.scenarios.every(nonBlank),
      `${row.id} scenarios are required`,
    )
    assert(Array.isArray(row.artifacts), `${row.id} artifacts must be an array`)
    assert(nonBlank(row.notes), `${row.id} notes are required`)

    if (row.status === 'PASS') {
      assert(nonBlank(row.executed_at), `${row.id} PASS requires an execution timestamp`)
      assert(row.artifacts.length > 0, `${row.id} PASS requires artifacts`)
      for (const artifact of row.artifacts) {
        assert(nonBlank(artifact.path), `${row.id} artifact path is required`)
        assert(validSHA(artifact.sha256, 64), `${row.id} artifact SHA-256 is invalid`)
      }
      assert(
        row.mode === 'automated' ||
          (!row.operator.toLowerCase().includes('automated') &&
            row.operator.toLowerCase() !== 'codex'),
        `${row.id} human evidence cannot be passed by automation`,
      )
    } else {
      assert(row.executed_at === null, `${row.id} non-PASS row must not claim execution`)
    }
  }
  return matrix
}

async function validateArtifactFiles(matrix) {
  for (const row of matrix.rows) {
    for (const artifact of row.artifacts) {
      const path = resolve(webRoot, artifact.path)
      assert(path.startsWith(`${webRoot}/`), `${row.id} artifact escaped the web root`)
      const source = await readFile(path)
      const digest = createHash('sha256').update(source).digest('hex')
      assert(digest === artifact.sha256, `${row.id} artifact hash does not match ${artifact.path}`)
    }
  }
}

async function main() {
  const matrixPath = resolve(webRoot, process.argv[2] ?? 'evidence/accessibility-matrix.json')
  const matrix = validateAccessibilityEvidence(JSON.parse(await readFile(matrixPath, 'utf8')))
  await validateArtifactFiles(matrix)
  process.stdout.write(
    `${JSON.stringify({
      status: 'PASS',
      source_sha: matrix.source_sha,
      rows: matrix.rows.length,
      passed: matrix.rows.filter((row) => row.status === 'PASS').map((row) => row.id),
    })}\n`,
  )
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await main()
}
