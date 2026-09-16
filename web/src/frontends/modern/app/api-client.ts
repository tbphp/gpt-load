import {
  createApiClient,
  type ApiClientWithResponse,
  type ApiRequestOptions,
} from '@shared/http/client'
import type { AppLocale } from '@shared/http/types'
import type { AuthSession } from '@modern/features/auth/auth-session'

export function createSessionApiClient(deps: {
  fetch: typeof fetch
  getLocale(): AppLocale
  getSession(): AuthSession | undefined
}): ApiClientWithResponse {
  function forRequest(options?: ApiRequestOptions) {
    const session = deps.getSession()
    const revision = session?.getRevision()
    const credential = session?.getAuthKey() ?? ''
    return createApiClient({
      fetch: deps.fetch,
      getAuthKey: () => credential,
      getLocale: deps.getLocale,
      onUnauthorized: () => {
        // 已退出或更换身份后，旧请求的 401 不得清除新会话。
        if (!options?.signal?.aborted && revision === session?.getRevision()) session?.clear()
      },
    })
  }
  return {
    request: (path, options) => forRequest(options).request(path, options),
    requestWithResponse: (path, options) => forRequest(options).requestWithResponse(path, options),
  }
}
