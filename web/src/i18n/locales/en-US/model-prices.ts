export default {
  modelPrices: {
    title: 'Model prices',
    back: 'Back to settings',
    loading: 'Loading model prices…',
    loadFailed: 'Unable to load model prices',
    stale: 'The current list may be stale because the background refresh failed',
    context:
      'Prices use USD per 1M tokens and affect future cost estimates only; historical requests are not recalculated',
    result: 'Showing {shown} of {total}',
    filters: {
      region: 'Filter model prices',
      searchLabel: 'Search',
      searchPlaceholder: 'Model ID, provider, or Group',
      clearSearch: 'Clear search',
      usageLabel: 'Usage',
      statusLabel: 'Pricing status',
      reset: 'Reset filters',
      usage: {
        in_use: 'In use',
        unreferenced: 'Unreferenced',
        all: 'All',
      },
      status: {
        all: 'All',
        pending: 'Pending',
        configured: 'Configured',
      },
    },
    sync: {
      action: 'Sync prices',
      succeeded: 'The model catalog and automatic prices are synced',
      failed: 'Unable to sync the model catalog and prices',
    },
    empty: {
      title: 'No in-use model prices yet',
      description: 'Sync the model catalog or configure models in a Group to get started',
      noResultsTitle: 'No matching model prices',
      noResultsDescription: 'Adjust the search, usage, or pricing-status filters and try again',
    },
    sections: {
      count: '{count} entries',
      pending: {
        title: 'Pending pricing',
        description: 'No price is currently available for cost estimation',
        tableLabel: 'Pending model prices',
      },
      configured: {
        title: 'Configured pricing',
        description: 'Current four-slot prices and their ownership method',
        tableLabel: 'Configured model prices',
      },
    },
    columns: {
      identity: 'Model / scope',
      status: 'Status',
      prices: 'Prices · USD / 1M',
      facts: 'Method / references',
      updatedAt: 'Updated',
      actions: 'Actions',
    },
    status: {
      pending: 'Pending',
      configured: 'Configured',
    },
    scope: {
      provider: 'Provider',
      group: 'Group',
    },
    fields: {
      input: 'Input',
      output: 'Output',
      cache_read: 'Cache read',
      cache_write: 'Cache write',
    },
    values: {
      unavailable: 'Unavailable',
      free: 'Free',
      configured: '${value}',
    },
    method: {
      pending: 'No pricing method',
      auto_sync: 'Synced from Models.dev',
      auto_matched: 'Reference price: {provider}',
      user_override: 'Manual override',
      user_set: 'Manually set',
      user_marked_unpriced: 'Manually marked unpriced',
    },
    reference: 'Reference price: {provider}',
    references: '{entries} references · {groups} Groups',
    facts: {
      partial: 'Incomplete upstream pricing',
      tiered: 'Context-tier pricing available',
    },
    edit: {
      open: 'Edit prices for {model}',
    },
    drawer: {
      title: 'Edit model prices',
      description: 'Set four price slots; empty is unavailable and 0 is explicitly free',
      close: 'Close the model price editor',
      identity: 'Model identity',
      identityDescription: 'The server owns the model and price scope; they cannot be edited here',
      model: 'Model ID',
      scope: 'Price scope',
      currentStatus: 'Current status',
      reference: 'Price reference',
      prices: 'Price slots',
      priceDescription:
        'Displayed and editable prices are cost estimates in USD per 1M tokens, not actual upstream billing',
      unit: 'USD / 1M',
      tierNote:
        'This price has context tiers; saving manually replaces the tiered price with these four slots',
      partialNote: 'Upstream pricing is incomplete; review each unavailable slot',
      availableState: 'Empty is unavailable; 0 is free',
      unpricedState: 'Leaving every slot empty marks this model unpriced',
      save: 'Save prices',
      saveFailed: 'Unable to save model prices; your input is preserved',
      errors: {
        invalid_price:
          'Enter a non-negative price with at most 9 decimals within the supported range',
      },
      unpricedConfirm: {
        title: 'Mark this model unpriced?',
        description: 'All four price slots for “{model}” will become unavailable',
        close: 'Close unpriced confirmation',
        warning:
          'Future requests still record tokens, but cost is estimated as 0 and marked unpriced',
        confirm: 'Confirm unpriced',
      },
    },
    reset: {
      open: 'Reset prices',
      title: 'Reset prices?',
      description: 'Reset the manual price settings for “{model}”',
      close: 'Close price reset confirmation',
      warning:
        'Automatic prices are restored when available; otherwise the model returns to pending',
      confirm: 'Reset prices',
      failed: 'Unable to reset prices',
    },
    delete: {
      open: 'Delete price entry',
      title: 'Delete this price entry?',
      description: 'Delete the unreferenced manual price entry for “{model}”',
      close: 'Close price deletion confirmation',
      warning: 'Only unreferenced manual entries can be deleted',
      confirm: 'Delete entry',
      failed: 'Unable to delete the price entry',
    },
    errors: {
      referenced: 'This price has {entries} references across {groups} Groups',
      automaticDeleteForbidden:
        'Automatic prices cannot be deleted; make this a manual entry first',
    },
  },
} as const
