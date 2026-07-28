import { controlQueryKeys } from '@/app/query-keys'

const metadataFields = [
  'id',
  'name',
  'masked_key',
  'status',
  'filters',
  'rpm_limit',
  'created_at',
  'updated_at',
] as const

const optionFields = ['id', 'name', 'status'] as const

export const accessKeyResources = {
  list: {
    queryKey: controlQueryKeys.accessKeys.list(),
    gcTime: 0,
    cleanup: 'authenticated-session',
    optimisticUpdates: false,
    allowedFields: metadataFields,
  },
  options: {
    queryKey: controlQueryKeys.accessKeys.options(),
    gcTime: 0,
    cleanup: 'authenticated-session',
    optimisticUpdates: false,
    allowedFields: optionFields,
  },
} as const

export const accessKeyMutationInvalidations = {
  create: [accessKeyResources.list.queryKey, accessKeyResources.options.queryKey],
  update: [accessKeyResources.list.queryKey, accessKeyResources.options.queryKey],
  delete: [accessKeyResources.list.queryKey, accessKeyResources.options.queryKey],
  reveal: [],
} as const
