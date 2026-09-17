import {
  gatewayClients,
  gatewayTargets,
  type GatewayClientID,
  type GatewayTargetID,
} from './gateway-config'

interface GatewayPreferences {
  client: GatewayClientID
  accessKeyID: number
  target: GatewayTargetID
  model: string
}

const defaults: GatewayPreferences = {
  client: 'codex',
  accessKeyID: 0,
  target: 'claude',
  model: '',
}
const memory = new Map<string, GatewayPreferences>()
const storageKey = (admin: boolean) =>
  'gpt-load.modern.home-connection.' + (admin ? 'admin' : 'access-key')

export function readGatewayPreferences(admin: boolean): GatewayPreferences {
  const key = storageKey(admin)
  try {
    const raw: unknown = JSON.parse(window.localStorage.getItem(key) ?? 'null')
    if (!raw || typeof raw !== 'object') return memory.get(key) ?? { ...defaults }
    const value = raw as Record<string, unknown>
    return {
      client: gatewayClients.find((client) => client.id === value.client)?.id ?? defaults.client,
      accessKeyID:
        admin &&
        typeof value.accessKeyID === 'number' &&
        Number.isSafeInteger(value.accessKeyID) &&
        value.accessKeyID > 0
          ? value.accessKeyID
          : 0,
      target: gatewayTargets.find((target) => target.id === value.target)?.id ?? defaults.target,
      model: typeof value.model === 'string' ? value.model : '',
    }
  } catch {
    return memory.get(key) ?? { ...defaults }
  }
}

export function rememberGatewayPreferences(admin: boolean, value: GatewayPreferences): void {
  const key = storageKey(admin)
  // 只记住选择及密钥标识，不保存密钥正文；禁用浏览器存储时仍能跨页面恢复。
  memory.set(key, { ...value })
  try {
    window.localStorage.setItem(key, JSON.stringify(value))
  } catch {
    return
  }
}
