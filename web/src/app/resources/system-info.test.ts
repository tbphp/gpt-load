import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import { getSystemInfo, projectSystemInfo } from './system-info'

const info = {
  version: '2.0.0-dev',
  deployment: {
    instance_mode: 'single',
    database: 'sqlite',
    distribution: 'single_binary',
  },
  data_dir: './data',
  auth_key: { source: 'key_file', path: 'data/auth.key' },
  encryption: { enabled: true, source: 'environment', path: null },
} as const

describe('SystemInfo resource', () => {
  it('projects the fixed version/deployment metadata and transport response', async () => {
    expect(projectSystemInfo(info)).toEqual(info)
    const request = vi.fn().mockResolvedValue(info) as ApiClient['request']
    await expect(getSystemInfo({ request })).resolves.toEqual(info)
  })

  it.each([
    { ...info, version: '' },
    { ...info, deployment: { ...info.deployment, database: 'postgres' } },
    { ...info, auth_key: { source: 'environment', path: 'secret/path' } },
    { ...info, encryption: { enabled: false, source: 'environment', path: null } },
    { ...info, raw_encryption_key: 'CANARY' },
  ])('rejects unsafe system metadata %#j', (unsafe) => {
    expect(() => projectSystemInfo(unsafe)).toThrow(InvalidResponseError)
  })
})
