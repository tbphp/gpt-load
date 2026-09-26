export const codexLiveModes = ['off', 'direct', 'relay'] as const
export type CodexLiveMode = (typeof codexLiveModes)[number]
