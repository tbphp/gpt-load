export default {
  models: {
    title: 'Models',
    loading: 'Loading models…',
    loadFailed: 'Unable to load models',
    stale: 'The current model information may be stale because the background refresh failed',
    context:
      'Prices use USD per 1M tokens for future cost estimates only; they are not actual upstream billing',
    result: 'Showing {shown} of {total} client models',
    actions: {
      sync: 'Sync models',
    },
    sync: {
      succeeded: 'The model catalog and automatic prices are synced',
      failed: 'Unable to sync the model catalog and prices',
    },
    status: {
      models: '{count} models',
      pending: '{count} pending',
      unit: 'USD / 1M tokens (estimate)',
    },
    catalog: {
      available: 'Models.dev catalog available',
      stale: 'Sync failed; using the last successful catalog',
      unavailable: 'Models.dev catalog unavailable',
      lastSuccess: 'Last sync',
      lastCheck: 'Last check',
    },
    filters: {
      region: 'Filter models',
      searchLabel: 'Search',
      searchPlaceholder: 'Client model, upstream model, provider, or Group',
      clearSearch: 'Clear search',
      groupStatusLabel: 'Group status',
      pricingStatusLabel: 'Pricing status',
      reset: 'Reset filters',
      groupStatus: {
        enabled: 'Enabled Groups',
        all: 'All Groups',
      },
      pricingStatus: {
        all: 'All prices',
        pending: 'Pending',
        configured: 'Configured',
      },
    },
    empty: {
      title: 'No models to display',
      description: 'Configure models in an enabled Group or sync the model catalog to get started',
      noResultsTitle: 'No matching models',
      noResultsDescription: 'Adjust the search, Group status, or pricing status and try again',
    },
    index: {
      label: 'Client model list',
      priceCount: '{count} price sets',
      upstreamCount: '{count} upstreams',
      copy: 'Copy client model {model}',
      copySucceeded: 'Client model copied',
      copyFailed: 'Unable to copy the client model',
    },
    inspector: {
      direct: 'Passed through by the same name',
      alias: 'Mapped to {model}',
      noCatalog: 'No Models.dev catalog information',
      scopeOption: '{label} {kind}',
    },
    detail: {
      upstreamModel: 'Upstream model',
      routeGroups: 'Route Groups',
      groupEnabled: 'Enabled',
      groupDisabled: 'Disabled',
      globalImpact: 'This price affects {count} Groups in total',
      catalogReference: {
        actual_provider: 'Models.dev · {provider}',
        reference_provider: 'Models.dev reference provider · {provider}',
      },
      specs: {
        context: 'Context',
        maxOutput: 'Max output',
        modalities: 'Modalities',
      },
      capabilities: {
        attachment: 'Attachments',
        reasoning: 'Reasoning',
        tool_call: 'Tool calls',
        structured_output: 'Structured output',
        temperature: 'Temperature',
      },
      openWeights: 'Open weights',
    },
  },
} as const
