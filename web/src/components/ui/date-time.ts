export function resolveLocalTimeZone(
  resolve: () => string = () => new Intl.DateTimeFormat().resolvedOptions().timeZone,
): string {
  try {
    return resolve() || 'UTC'
  } catch {
    return 'UTC'
  }
}
