import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import { getSystemInfo, projectSystemInfo } from './system-info'

const safeResponse = {
  version: '2.0.0',
  deployment: {
    instance_mode: 'single',
    database: 'sqlite',
    distribution: 'single_binary',
  },
  data_dir: '/var/lib/gpt-load',
  auth_key: { source: 'key_file', path: '/var/lib/gpt-load/auth.key' },
  encryption: { enabled: true, source: 'environment', path: null },
} as const

describe('SystemInfo control API', () => {
  it('uses the exact GET path and AbortSignal', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue(safeResponse)

    await getSystemInfo({ request: request as ApiClient['request'] }, signal)

    expect(request).toHaveBeenCalledWith('/api/system/info', { signal })
  })

  it('fails closed when system metadata contains non-contract or secret-like fields', () => {
    expect(() =>
      projectSystemInfo({
        ...safeResponse,
        auth_key: {
          ...safeResponse.auth_key,
          value: 'AUTH_KEY_CONTENT_CANARY',
          hash: 'AUTH_KEY_HASH_CANARY',
        },
        encryption: {
          ...safeResponse.encryption,
          key: 'ENCRYPTION_KEY_CANARY',
          ciphertext: 'CIPHERTEXT_CANARY',
        },
        database_dsn: 'SECRET_DSN_CANARY',
        debug_dump: { token: 'TOKEN_CANARY' },
      }),
    ).toThrow(InvalidResponseError)
  })

  it('rejects non-fixed deployment metadata and disabled encryption generically', () => {
    expect(() =>
      projectSystemInfo({
        ...safeResponse,
        deployment: { ...safeResponse.deployment, database: 'postgres' },
      }),
    ).toThrow(InvalidResponseError)
    expect(() =>
      projectSystemInfo({
        ...safeResponse,
        encryption: { ...safeResponse.encryption, enabled: false },
      }),
    ).toThrow(InvalidResponseError)
  })
})
