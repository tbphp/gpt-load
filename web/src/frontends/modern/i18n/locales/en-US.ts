export default {
  sections: {
    workspace: 'Workspace',
    observe: 'Observability',
    system: 'System',
  },
  shell: {
    goHome: 'GPT-Load overview',
    collapseSidebar: 'Collapse sidebar',
    expandSidebar: 'Expand sidebar',
    close: 'Close',
    documentation: 'Documentation',
    selfHosted: 'Self-hosted AI gateway',
    mobileNavigationDescription: 'Open a management workspace or observability page.',
    preview: 'Framework preview',
    previewDescription:
      'Business data is not connected yet. Full management features remain available in Classic.',
  },
  pages: {
    home: {
      title: 'Overview',
      description: 'Manage your AI gateway, from configuration to observability.',
    },
    groups: {
      title: 'Groups',
      description: 'Manage upstream connections, credentials, models, and group settings.',
    },
    models: {
      title: 'Models',
      description: 'Explore model availability, routing capabilities, and pricing.',
    },
    accessKeys: {
      title: 'Access keys',
      description: 'Manage client access, scopes, and usage limits.',
    },
    usage: {
      title: 'Usage',
      description: 'Track requests, token usage, and estimated costs.',
    },
    logs: {
      title: 'Request logs',
      description: 'Inspect request outcomes, upstream attempts, and usage details.',
    },
    health: {
      title: 'Runtime health',
      description: 'Check credential availability, cooldowns, and runtime issues.',
    },
    inspector: {
      title: 'Route inspection',
      description: 'Understand how models match groups and upstream credentials.',
    },
    settings: {
      title: 'Global settings',
      description: 'Choose appearance, language, and interface preferences for this browser.',
    },
  },
  home: {
    manageGroups: 'Manage groups',
    workspace: {
      title: 'Resources & access',
      description: 'Configure upstream capabilities and client access.',
    },
    observe: {
      title: 'Observability',
      description: 'Go directly from usage trends to individual requests.',
    },
    workflowTitle: 'Connect your gateway',
    workflowDescription: 'Connect upstream services and give clients a unified endpoint.',
    workflowGroups: 'Create a group',
    workflowCredentials: 'Configure credentials & models',
    workflowAccess: 'Create an access key',
  },
  workspace: {
    pendingTitle: 'This workspace is under development',
    pendingDescription:
      'Navigation is ready. Business data and actions will be added in upcoming iterations.',
    backHome: 'Back to overview',
  },
  appearance: {
    title: 'Appearance & preferences',
    description: 'Applies only to this browser and does not change gateway configuration.',
    theme: 'Display mode',
    themeDescription: 'Choose a light or dark appearance, or follow your system.',
    language: 'Interface language',
    languageDescription: 'Changes take effect immediately.',
    themes: {
      system: 'System',
      light: 'Light',
      dark: 'Dark',
    },
    persistenceFailed: 'Browser storage is unavailable. Your preferences apply to this visit only.',
  },
  quickNavigation: {
    title: 'Quick navigation',
    placeholder: 'Find pages & features…',
    description: 'Search for a page, use the arrow keys to select it, and press Enter.',
    pages: 'Pages & features',
    empty: 'No matching pages.',
    keyboardHint: '↑ ↓ Select　Enter Open　Esc Close',
  },
  navigation: 'Main navigation',
  skipToContent: 'Skip to main content',
  settings: 'Global settings',
  interfaceSettings: 'Interface',
  homeTitle: 'New interface',
  homeDescription:
    'The new interface is under development. Full management features are available in Classic.',
  unavailableTitle: 'This page is not yet available in the new interface',
  frontend: {
    description:
      'Choose the interface for this browser. Switching reloads the page and does not affect other visitors.',
    current: 'Current interface',
    previewNote:
      'Thumbnails are schematic. The new preview will be updated when its design is ready.',
    saveFailed: 'Unable to save your preference. Allow browser storage for this site and retry.',
    modern: {
      title: 'New',
      description: 'A new management interface, currently under development.',
    },
    classic: {
      title: 'Classic',
      description: 'The existing layout with full management features.',
    },
  },
}
