export default {
  common: {
    appName: 'GPT-Load',
    retry: 'Retry',
    changeKey: 'Change AUTH_KEY',
  },
  auth: {
    loginTitle: 'Sign in to the admin interface',
    loginDescription: 'Enter the management AUTH_KEY to access the control plane.',
    keyLabel: 'AUTH_KEY',
    keyPlaceholder: 'Enter AUTH_KEY',
    submit: 'Sign in',
    submitting: 'Verifying…',
    required: 'Enter an AUTH_KEY',
    invalidFormat: 'AUTH_KEY cannot contain whitespace',
    invalid: 'The AUTH_KEY is invalid',
    locked: 'Too many authentication attempts. Try again in {seconds} seconds.',
    network: 'Unable to reach the management API. Check the service and try again.',
    invalidResponse: 'The management API returned an unrecognized response.',
    checking: 'Verifying the current session…',
    whereTitle: 'Where is the AUTH_KEY?',
    whereBody:
      'Set the AUTH_KEY environment variable, or leave it empty and the service will generate DATA_DIR/auth.key. Logs show only the file path, never the full key.',
    dockerHint:
      "With Docker Compose, run: docker compose exec gpt-load sh -c 'cat /app/data/auth.key'",
  },
} as const
