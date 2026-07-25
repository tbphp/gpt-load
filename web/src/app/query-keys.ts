export const controlQueryKeys = {
  all: ['control'] as const,
  groups: {
    all: ['control', 'groups'] as const,
    list: () => ['control', 'groups', 'list'] as const,
    details: () => ['control', 'groups', 'detail'] as const,
    detail: (id: number) => ['control', 'groups', 'detail', id] as const,
    keyLists: () => ['control', 'groups', 'keys'] as const,
    keys: (id: number) => ['control', 'groups', 'keys', id] as const,
  },
  health: () => ['control', 'health'] as const,
  accessKeys: {
    list: () => ['control', 'access-keys', 'list'] as const,
  },
  settings: () => ['control', 'settings'] as const,
  systemInfo: () => ['control', 'system-info'] as const,
}
