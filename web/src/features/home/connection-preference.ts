const connectionPreferenceKey = 'gpt-load.connection.expanded'

export interface ConnectionPreferenceStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

export interface ConnectionPreference {
  initialExpanded: boolean
  setExpanded(expanded: boolean): void
}

export function createConnectionPreference(
  storage?: ConnectionPreferenceStorage,
): ConnectionPreference {
  let initialExpanded = true
  try {
    initialExpanded = storage?.getItem(connectionPreferenceKey) !== 'false'
  } catch {
    initialExpanded = true
  }

  return {
    initialExpanded,
    setExpanded(expanded) {
      try {
        storage?.setItem(connectionPreferenceKey, String(expanded))
      } catch {
        // The current in-memory choice remains usable when persistence is denied.
      }
    },
  }
}

export function resolveConnectionPreferenceStorage(): ConnectionPreferenceStorage | undefined {
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}
