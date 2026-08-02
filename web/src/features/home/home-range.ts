import type { HomeRange } from '@/app/resources/home'

type HomeRangeLabelKey =
  | 'home.range.display24Hours'
  | 'home.range.display30Days'

export function homeRangeLabelKey(range: HomeRange): HomeRangeLabelKey {
  return range === '24h' ? 'home.range.display24Hours' : 'home.range.display30Days'
}
