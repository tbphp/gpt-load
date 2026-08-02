import type { ApiClient } from '@/api/client'
import type { AccessKeyDto } from '@/api/control/types'
import {
  listAccessKeyCollection,
  type UpdateAccessKeyRequest,
} from '@/app/resources/access-keys'

export interface PendingAccessKeyEditOperation {
  base: AccessKeyDto
  patch: UpdateAccessKeyRequest
  state: 'indeterminate' | 'reconciling'
}

export async function findAccessKeyForReconciliation(
  client: ApiClient,
  id: number,
  signal: AbortSignal,
): Promise<AccessKeyDto | undefined> {
  const firstPage = await listAccessKeyCollection(client, { page: 1, page_size: 20 }, signal)
  const expectedSummary = firstPage.summary
  const expectedPagination = firstPage.pagination
  const records = new Map<number, AccessKeyDto>()

  function capturePage(page: typeof firstPage): void {
    if (
      page.summary.total !== expectedSummary.total ||
      page.summary.active !== expectedSummary.active ||
      page.summary.disabled !== expectedSummary.disabled ||
      page.pagination.total_items !== expectedPagination.total_items ||
      page.pagination.total_pages !== expectedPagination.total_pages ||
      page.pagination.page_size !== expectedPagination.page_size
    ) {
      throw new Error('ACCESS_KEY_COLLECTION_CHANGED_DURING_RECONCILIATION')
    }
    for (const accessKey of page.items) {
      if (records.has(accessKey.id)) {
        throw new Error('ACCESS_KEY_COLLECTION_CHANGED_DURING_RECONCILIATION')
      }
      records.set(accessKey.id, accessKey)
    }
  }

  capturePage(firstPage)
  for (let page = 2; page <= expectedPagination.total_pages; page += 1) {
    const result = await listAccessKeyCollection(client, { page, page_size: 20 }, signal)
    if (result.pagination.page !== page) {
      throw new Error('ACCESS_KEY_COLLECTION_CHANGED_DURING_RECONCILIATION')
    }
    capturePage(result)
  }
  if (records.size !== expectedPagination.total_items) {
    throw new Error('ACCESS_KEY_COLLECTION_CHANGED_DURING_RECONCILIATION')
  }
  return records.get(id)
}
