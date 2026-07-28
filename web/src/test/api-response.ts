import type { ApiClient, ApiClientWithResponse, ApiPath, ApiRequestOptions } from '@/api/client'

const getToken = `sha256-${'a'.repeat(64)}`
const putToken = `sha256-${'b'.repeat(64)}`

export function apiWithResponseMetadata(
  request: ApiClient['request'],
  tokenFor: (path: ApiPath, options: ApiRequestOptions | undefined, data: unknown) => string = (
    _path,
    options,
  ) => (options?.method === 'PUT' ? putToken : getToken),
): ApiClientWithResponse {
  return {
    request,
    async requestWithResponse<T>(path: ApiPath, options?: ApiRequestOptions) {
      const data = await request<T>(path, options)
      const token = tokenFor(path, options, data)
      return {
        data,
        status: 200,
        headers: new Headers({ ETag: `"${token}"` }),
      }
    },
  }
}

export const testSettingsETags = {
  get: getToken,
  put: putToken,
} as const
