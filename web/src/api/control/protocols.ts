import type { AccessProtocol, GroupProtocol } from './types'

export const knownAccessProtocols = [
  'openai',
  'anthropic',
  'gemini',
  'openai-response',
] as const satisfies readonly AccessProtocol[]

export const enabledDataProtocols = [
  'openai',
  'anthropic',
  'gemini',
] as const satisfies readonly GroupProtocol[]
