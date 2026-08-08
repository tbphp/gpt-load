import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import {
  isCanonicalRouteQuery,
  parsePositiveRouteInteger,
  scalarRouteQuery,
} from '@/app/route-query'
import type { HomeRange } from '@/app/resources/home'

import { gatewayClients, type GatewayClientID } from './gateway-clients'

export type HomeRankingDimension = 'models' | 'groups' | 'accessKeys'

export interface HomeRouteState {
  range: HomeRange
  ranking: HomeRankingDimension
  accessKeyID?: number
  client: GatewayClientID
}

const rankingValues: Record<string, HomeRankingDimension> = {
  models: 'models',
  groups: 'groups',
  'access-keys': 'accessKeys',
}

export function parseHomeRouteQuery(query: LocationQuery): HomeRouteState {
  const rawRange = scalarRouteQuery(query.range)
  const rawRanking = scalarRouteQuery(query.ranking)
  const rawClient = scalarRouteQuery(query.client)
  return {
    range: rawRange === '30d' ? '30d' : '24h',
    ranking: rawRanking === undefined ? 'models' : (rankingValues[rawRanking] ?? 'models'),
    accessKeyID: parsePositiveRouteInteger(query.access_key_id),
    client:
      gatewayClients.find(({ id }) => id === rawClient)?.id ?? ('nextchat' as GatewayClientID),
  }
}

export function serializeHomeRouteQuery(state: HomeRouteState): LocationQueryRaw {
  const query: LocationQueryRaw = {}
  if (state.range !== '24h') query.range = state.range
  if (state.ranking === 'groups') query.ranking = 'groups'
  if (state.ranking === 'accessKeys') query.ranking = 'access-keys'
  if (state.accessKeyID !== undefined) query.access_key_id = String(state.accessKeyID)
  if (state.client !== 'nextchat') query.client = state.client
  return query
}

export function isCanonicalHomeRouteQuery(query: LocationQuery, state: HomeRouteState): boolean {
  return isCanonicalRouteQuery(query, serializeHomeRouteQuery(state))
}
