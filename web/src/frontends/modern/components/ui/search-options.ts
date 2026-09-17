import type { SearchSelectOption } from './types'
const normalize = (value: string) =>
  value
    .normalize('NFKD')
    .replace(/\p{Diacritic}/gu, '')
    .toLocaleLowerCase()
export function matchesSearchOption(option: SearchSelectOption, query: string): boolean {
  const terms = normalize(query).trim().split(/\s+/u).filter(Boolean)
  const content = normalize([option.label, option.value, ...(option.keywords ?? [])].join(' '))
  return terms.every((term) => content.includes(term))
}
