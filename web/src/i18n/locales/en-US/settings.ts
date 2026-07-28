export default {
  settings: {
    title: 'Settings',
    description:
      'Manage browser-local preferences, global runtime behavior, and safe system metadata.',
    loading: 'Loading runtime settings…',
    loadFailed: 'Unable to load runtime settings.',
    stale: 'Runtime settings may be stale because the background refresh failed.',
    save: 'Save changes',
    saved: 'Settings saved.',
    effectiveValue: 'Effective value: {value}',
    override: 'Override',
    default: 'Code default',
    useOverride: 'Persist an explicit override',
    outcome: {
      reconciling: 'The save result is unknown. Checking the latest server state…',
      indeterminate:
        'The save result could not be confirmed. Do not save again until the latest state is checked.',
      checkResult: 'Check result',
    },
    conflict: {
      rebased:
        'Settings changed elsewhere. The latest values were merged with your non-conflicting edits; review and save again.',
      blocked:
        'Settings changed elsewhere and overlap with your edits. Resolve every mine/latest conflict before saving.',
      mine: 'Mine',
      latest: 'Latest',
      useMine: 'Use mine',
      useLatest: 'Use latest',
      headerRulesSummary: '{set} Set and {remove} Remove rules',
    },
    appearance: {
      title: 'Appearance',
      description: 'Choose the interface language and color theme.',
      locale: 'Language',
      theme: 'Theme',
      localOnly:
        'These preferences apply only to the current browser and are not sent to runtime Settings.',
    },
    request: {
      title: 'Request and forwarding',
      description: 'Control global timeout and upstream header behavior.',
      connect_timeout: 'Connect timeout',
      first_byte_timeout: 'First-byte timeout',
      request_timeout: 'Request timeout',
      stream_idle_timeout: 'Stream idle timeout',
      seconds: 'Seconds; use a positive whole number.',
      timeoutError: 'Enter a positive safe integer.',
      headerRules: 'Advanced HeaderRules',
      headerSummary: '{set} Set and {remove} Remove rules are effective.',
      headerWarning:
        'Global rules affect every Group without a HeaderRules override. A Group override replaces the complete global object.',
      saveFailed: 'Unable to update request and forwarding settings. Your input is unchanged.',
    },
    logs: {
      title: 'Logs and maintenance',
      description: 'Control automatic request-log retention.',
      retention: 'Request-log retention days',
      retentionDescription: 'Keep persisted request logs for 1 through 365 days.',
      retentionError: 'Enter a whole number between 1 and 365.',
      saveFailed: 'Unable to update log retention. Your input is unchanged.',
    },
    system: {
      title: 'System information',
      description: 'Read-only deployment and secret-source metadata.',
      loading: 'Loading system information…',
      loadFailed: 'Unable to load system information.',
      stale: 'System information may be stale because the background refresh failed.',
      version: 'Version',
      deployment: 'Deployment',
      single: 'Single instance',
      sqlite: 'SQLite',
      singleBinary: 'Single binary',
      dataDir: 'Data directory',
      authKey: 'AUTH_KEY source',
      encryption: 'Encryption',
      enabled: 'Enabled',
      copyPath: 'Copy path',
      sources: {
        environment: 'Environment variable',
        key_file: 'Key file',
      },
      securityNote:
        'Only non-secret paths can be copied. AUTH_KEY and encryption-key contents are never shown.',
    },
  },
} as const
