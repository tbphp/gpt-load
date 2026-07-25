import type { UpstreamKeyPatch, UpstreamKeyStatus } from '@/api/control/upstream-keys'

export interface UpstreamKeyEditable {
  status: UpstreamKeyStatus
  weight_manual: number | null
}

function validateWeight(value: number | null): void {
  if (value !== null && (!Number.isInteger(value) || value < 1 || value > 100)) {
    throw new Error('INVALID_UPSTREAM_KEY_WEIGHT')
  }
}

export function buildUpstreamKeyPatch(
  base: UpstreamKeyEditable,
  next: UpstreamKeyEditable,
): UpstreamKeyPatch {
  validateWeight(base.weight_manual)
  validateWeight(next.weight_manual)
  if (next.status !== 'active' && next.status !== 'disabled') {
    throw new Error('INVALID_UPSTREAM_KEY_STATUS')
  }

  const patch: UpstreamKeyPatch = {}
  if (next.status !== base.status) patch.status = next.status
  if (next.weight_manual !== base.weight_manual) patch.weight_manual = next.weight_manual
  return patch
}
