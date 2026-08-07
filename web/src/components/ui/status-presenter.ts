export type StatusTone = 'success' | 'warning' | 'danger' | 'neutral'
export type StatusIcon = 'check' | 'alert' | 'off' | 'progress' | 'help' | 'edit'

export type OperationalStatus =
  'available' | 'cooldown' | 'blacklisted' | 'disabled' | 'unavailable' | 'unknown'

export type MutationStatus = 'confirmed' | 'failed' | 'indeterminate' | 'reconciling'

export interface StatusPresentation {
  tone: StatusTone
  icon: StatusIcon
}

const operationalPresentations: Record<OperationalStatus, StatusPresentation> = {
  available: { tone: 'success', icon: 'check' },
  cooldown: { tone: 'warning', icon: 'alert' },
  blacklisted: { tone: 'danger', icon: 'off' },
  disabled: { tone: 'neutral', icon: 'off' },
  unavailable: { tone: 'danger', icon: 'off' },
  unknown: { tone: 'neutral', icon: 'help' },
}

const mutationPresentations: Record<MutationStatus, StatusPresentation> = {
  confirmed: { tone: 'success', icon: 'check' },
  failed: { tone: 'danger', icon: 'off' },
  indeterminate: { tone: 'warning', icon: 'alert' },
  reconciling: { tone: 'warning', icon: 'progress' },
}

export function presentOperationalStatus(status: OperationalStatus): StatusPresentation {
  return operationalPresentations[status]
}

export function presentMutationStatus(status: MutationStatus): StatusPresentation {
  return mutationPresentations[status]
}
