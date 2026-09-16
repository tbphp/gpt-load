import type { ApiClient } from '@shared/http/client'
import { protocolOrder } from '@modern/i18n/protocols'
import { routeStrategies, type RouteStrategy } from './settings'
import { boolean, integer, list, oneOf, record, text } from './response'

export interface InspectionRequest {
  protocol: (typeof protocolOrder)[number]
  external_model: string
  access_key_id: number
}
export interface InspectionCredential {
  id: number
  available: boolean
  reason: string | null
  weight: number
  effectiveWeight: number
  cooldownUntil: number | null
}
export interface InspectionGroup {
  id: number
  name: string
  channelID: string
  mode: 'native' | 'converted'
  requirementSatisfied: boolean
  model: string | null
  weight: number | null
  included: boolean
  routable: boolean
  reason: string | null
  credentials: InspectionCredential[]
}
export interface Inspection {
  observedAt: number
  revision: number
  strategy: RouteStrategy
  protocol: InspectionRequest['protocol']
  operation: string
  requirement: 'any' | 'native'
  model: string | null
  accessKey: { id: number; name: string; status: 'active' | 'disabled' }
  routable: boolean
  reason: string | null
  groups: InspectionGroup[]
}
const optionalText = (value: unknown) => (value === null ? null : text(value))
const optionalNumber = (value: unknown) => (value === null ? null : integer(value))

export async function inspectRoute(
  client: ApiClient,
  request: InspectionRequest,
  signal: AbortSignal,
): Promise<Inspection> {
  const data = record(
    await client.request('/api/route/inspect', { method: 'POST', json: request, signal }),
  )
  const key = record(data.access_key)
  return {
    observedAt: integer(data.observed_at_ms),
    revision: integer(data.snapshot_revision, 1),
    strategy: oneOf(data.route_strategy, routeStrategies),
    protocol: oneOf(data.protocol, protocolOrder),
    operation: text(data.operation),
    requirement: oneOf(data.route_requirement, ['any', 'native']),
    model: optionalText(data.external_model),
    accessKey: {
      id: integer(key.id, 1),
      name: text(key.name),
      status: oneOf(key.status, ['active', 'disabled']),
    },
    routable: boolean(data.routable),
    reason: optionalText(data.reason_code),
    groups: list(data.groups).map((value) => {
      const group = record(value)
      return {
        id: integer(group.group_id, 1),
        name: text(group.group_name),
        channelID: text(group.channel_id),
        mode: oneOf(group.route_mode, ['native', 'converted']),
        requirementSatisfied: boolean(group.route_requirement_satisfied),
        model: optionalText(group.upstream_model),
        weight: optionalNumber(group.weight_manual),
        included: boolean(group.included),
        routable: boolean(group.routable),
        reason: optionalText(group.reason_code),
        credentials: list(group.credentials).map((value) => {
          const credential = record(value)
          return {
            id: integer(credential.credential_id, 1),
            available: boolean(credential.available),
            reason: optionalText(credential.reason_code),
            weight: integer(credential.weight),
            effectiveWeight: integer(credential.effective_weight),
            cooldownUntil: optionalNumber(credential.cooldown_until_ms),
          }
        }),
      }
    }),
  }
}
