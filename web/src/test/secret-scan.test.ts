import { findSensitiveArtifacts } from '../../e2e/artifact-safety'

const staticFixtures = import.meta.glob('../../testdata/static-fixtures/**/*', {
  eager: true,
  import: 'default',
  query: '?raw',
}) as Record<string, string>

describe('generated artifact secret scan', () => {
  it('rejects generated AccessKey material', () => {
    const bytes = new TextEncoder().encode('sk-gl-0123456789abcdef0123456789abcdef')
    expect(findSensitiveArtifacts([{ path: 'trace.zip', bytes }], [])).toEqual(['trace.zip'])
  })

  it('keeps static fixtures free of generated AccessKeys', () => {
    const files = Object.entries(staticFixtures).map(([path, contents]) => ({
      path,
      bytes: new TextEncoder().encode(contents),
    }))
    expect(files.length).toBeGreaterThan(0)
    expect(findSensitiveArtifacts(files, [])).toEqual([])
  })
})
