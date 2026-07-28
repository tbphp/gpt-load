const cleaners = new Set<() => void>()

export function registerEphemeralStateCleaner(cleaner: () => void): () => void {
  cleaners.add(cleaner)
  return () => cleaners.delete(cleaner)
}

export function clearEphemeralState(): void {
  for (const cleaner of cleaners) cleaner()
}
