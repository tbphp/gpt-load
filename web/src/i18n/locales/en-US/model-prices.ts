export default {
  modelPrices: {
    status: {
      pending: 'Pending',
      configured: 'Configured',
      unpriced: 'Marked unpriced',
    },
    fields: {
      input: 'Input',
      output: 'Output',
      cache_read: 'Cache read',
      cache_write: 'Cache write 5m',
    },
    method: {
      pending: 'No pricing method',
      auto_sync: 'Synced from Models.dev',
      user_set: 'Manually set',
      user_marked_unpriced: 'Manually marked unpriced',
    },
    facts: {
      partial: 'Incomplete upstream pricing',
    },
    matrix: {
      heading: 'Price',
      unit: 'USD / 1M',
      thresholdColumn: 'Tier',
      baseRow: 'Base',
      tierRow: '≥ {threshold}',
      addTier: 'Add tier',
      removeTier: 'Remove this tier',
      replaceNote:
        'Tiers match the total input tokens of one request; reaching the threshold replaces the base price',
      oneHourRule: '1h cache write = 5m × 1.6',
      save: 'Save',
      saveFailed: 'Unable to save model prices; your input is preserved',
      versionConflict: 'This price changed. Cancel to load the latest value before editing again.',
      unpricedConfirm: {
        title: 'Mark this model unpriced?',
        description: 'All base and tier price slots for “{model}” will become unavailable',
        close: 'Close unpriced confirmation',
        warning:
          'Future requests still record tokens, but cost is estimated as 0 and marked unpriced',
        confirm: 'Confirm unpriced',
      },
      errors: {
        invalid_price:
          'Enter a non-negative price with at most 9 decimals within the supported range',
        threshold_required: 'Enter this tier’s input-quantity threshold',
        threshold_invalid: 'The threshold must be a non-negative integer',
        threshold_duplicate: 'The threshold must not repeat another tier',
        tier_empty: 'This tier needs at least one price',
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
