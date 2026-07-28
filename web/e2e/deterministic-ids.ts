import { createHash } from 'node:crypto'

export interface DeterministicTestIdentity {
  parallelIndex: number
  repeatEachIndex: number
  retry: number
  testId: string
}

export function deterministicUUIDPrefix(identity: DeterministicTestIdentity): string {
  const digest = createHash('sha256')
    .update(
      [identity.parallelIndex, identity.repeatEachIndex, identity.retry, identity.testId].join(':'),
    )
    .digest('hex')
  return `${digest.slice(0, 8)}-${digest.slice(8, 12)}-4${digest.slice(13, 16)}-8${digest.slice(17, 20)}-`
}
