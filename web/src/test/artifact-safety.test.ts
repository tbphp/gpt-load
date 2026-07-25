import { describe, expect, it } from 'vitest'

import { findSensitiveArtifacts } from '../../e2e/artifact-safety'

const encode = (value: string) => new TextEncoder().encode(value)

describe('findSensitiveArtifacts', () => {
  it('ignores artifacts without configured canaries or complete generated keys', () => {
    const files = [
      {
        path: 'safe.txt',
        bytes: encode('e2e-auth e2e-upstream sk-gl-0123456789abcdef'),
      },
    ]

    expect(findSensitiveArtifacts(files, ['e2e-auth-canary', 'e2e-upstream-key-one'])).toEqual([])
  })

  it('reports an artifact containing a configured canary', () => {
    const files = [
      {
        path: 'error.txt',
        bytes: encode('request failed for e2e-upstream-key-one'),
      },
    ]

    expect(findSensitiveArtifacts(files, ['e2e-auth-canary', 'e2e-upstream-key-one'])).toEqual([
      'error.txt',
    ])
  })

  it('reports an artifact containing a complete generated key', () => {
    const files = [
      {
        path: 'error.txt',
        bytes: encode('sk-gl-0123456789abcdef0123456789abcdef'),
      },
    ]

    expect(findSensitiveArtifacts(files, [])).toEqual(['error.txt'])
  })
})
