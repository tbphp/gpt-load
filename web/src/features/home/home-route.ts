import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import {
  isCanonicalRouteQuery,
  parsePositiveRouteInteger,
  scalarRouteQuery,
} from '@/app/route-query'

import { gatewayClients, type GatewayClientID } from './gateway-clients'

export interface HomeRouteState {
  accessKeyID?: number
  client: GatewayClientID
}

export function parseHomeRouteQuery(query: LocationQuery): HomeRouteState {
  const rawClient = scalarRouteQuery(query.client)
  return {
    accessKeyID: parsePositiveRouteInteger(query.access_key_id),
    client:
      gatewayClients.find(({ id }) => id === rawClient)?.id ?? ('nextchat' as GatewayClientID),
  }
}

export function serializeHomeRouteQuery(state: HomeRouteState): LocationQueryRaw {
  const query: LocationQueryRaw = {}
  if (state.accessKeyID !== undefined) query.access_key_id = String(state.accessKeyID)
  if (state.client !== 'nextchat') query.client = state.client
  return query
}

export function isCanonicalHomeRouteQuery(query: LocationQuery, state: HomeRouteState): boolean {
  return isCanonicalRouteQuery(query, serializeHomeRouteQuery(state))
}
