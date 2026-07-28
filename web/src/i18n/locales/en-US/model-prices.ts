export default {
  modelPrices: {
    title: 'Model prices',
    description:
      'Manage per-model prices used to estimate token costs. Built-in rules remain read-only.',
    back: 'Back to Settings',
    add: 'Add override',
    loading: 'Loading model prices…',
    loadFailed: 'Unable to load model prices.',
    stale: 'Model prices may be stale because the background refresh failed.',
    priceUnit: 'USD per one million tokens',
    historyNote:
      'Price changes affect future estimated costs. Historical usage and cost are not recalculated.',
    modelIdentityNote:
      'Rules match the upstream model ID after routing; the same model ID shares one global price across Groups.',
    precedenceNote: 'User overrides take precedence over every built-in exact and prefix rule.',
    wholeRuleNote:
      'A user override replaces the whole five-slot rule. Unset slots do not fall back to built-in values.',
    notConfigured: 'Not configured',
    explicitlyFree: '$0 · Explicitly free',
    configuredPrice: '${price} / 1M',
    globalUserOverride: 'Global user override',
    kind: {
      exact: 'Exact model',
      prefix: 'Prefix rule',
      global: 'Global rule',
    },
    source: {
      builtin: 'Built-in',
      user: 'Override',
    },
    fields: {
      uncached_input: 'Uncached input',
      cache_read: 'Cache read',
      cache_write_5m: '5-minute cache write',
      cache_write_1h: '1-hour cache write',
      output: 'Output',
    },
    table: {
      pattern: 'Model pattern',
      kind: 'Rule kind',
      source: 'Source',
      updatedAt: 'Updated',
      actions: 'Actions',
    },
    builtin: {
      title: 'Built-in prices',
      description: 'Read-only reference prices shipped with this version.',
      empty: 'No built-in model prices',
      emptyDescription: 'This build did not return any built-in price rules.',
      caption: 'Built-in model price rules',
      source: 'Official source',
      createOverride: 'Create override',
      longContext: {
        label: 'Long-context policy',
        summary:
          'More than {threshold} input tokens: input prices ×{inputMultiplier}; output price ×{outputMultiplier}.',
      },
    },
    overrides: {
      title: 'User overrides',
      description:
        'Each saved pattern replaces the complete matching rule with five explicit price slots.',
      empty: 'No overrides configured',
      emptyDescription: 'Add an exact, trailing-star prefix, or global override when needed.',
      caption: 'User model price overrides',
      edit: 'Edit',
    },
    drawer: {
      addTitle: 'Add model price override',
      builtinTitle: 'Create override from built-in price',
      editTitle: 'Edit model price override',
      description: 'Configure an exact, prefix, or global price rule.',
      close: 'Close model price editor',
      pattern: 'Model pattern',
      patternDescription:
        'Use an exact model ID, one trailing * for a prefix, or a bare * for every model.',
      patternReadonly: 'The pattern is fixed while editing. Add a new override to use another one.',
      prices: 'Estimated prices',
      priceDescription: 'USD per one million tokens. Empty means not configured; 0 is explicit.',
      wholeReplacement:
        'Saving replaces the whole rule for this pattern. All five price slots are submitted, including not configured values.',
      globalWarning:
        'All user rules take precedence over built-in rules. A bare * shadows every built-in price rule; more specific user rules still take precedence over *.',
      globalConfirm: 'I understand that * shadows every built-in price rule.',
      globalDialog: {
        title: 'Create a global user price override?',
        description:
          'A bare * changes the estimated price rule for every model without a more specific user override.',
        close: 'Close global price override confirmation',
        precedence:
          'This global user override takes precedence over every built-in exact and prefix rule.',
        noFallback:
          'Unset price slots do not fall back; the whole five-slot user rule replaces the built-in rule.',
        futureOnly:
          'The change applies only to future completed or Emit requests and does not recalculate history.',
        reset: 'Reset restores the remaining user and built-in rules for future requests.',
        confirm: 'Create global override',
      },
      save: 'Save override',
      saveFailed: 'Unable to save the model price override. Your input is unchanged.',
      errors: {
        required: 'Enter a model pattern.',
        too_long: 'Use at most 255 UTF-8 bytes.',
        surrounding_whitespace: 'Remove leading or trailing whitespace.',
        control_character: 'Control characters are not allowed.',
        question_mark: 'Question marks are not allowed.',
        star_position: 'Use at most one star, and only as the final character.',
        invalid_price: 'Enter a finite number greater than or equal to 0.',
        all_empty: 'Configure at least one of the five price slots.',
      },
    },
    reset: {
      open: 'Reset',
      title: 'Reset this model price override?',
      description: 'Delete the “{pattern}” override rule.',
      close: 'Close model price reset confirmation',
      warning:
        'After deletion, the next applicable user rule is used first, then a built-in rule. If no user or built-in rule matches, the model may be unpriced. Only this override is deleted; historical usage and cost are not recalculated.',
      confirm: 'Reset override',
      failed: 'Unable to reset the model price override.',
    },
    settingsEntry: {
      title: 'Model prices',
      description: 'Manage built-in references and explicit overrides used for estimated cost.',
      open: 'Model prices',
    },
  },
} as const
