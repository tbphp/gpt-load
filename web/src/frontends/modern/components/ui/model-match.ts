export function isModelPattern(value: string): boolean {
  const pattern = value.trim()
  return !pattern.includes('*') || /^[^*]+\*$/u.test(pattern)
}

export function matchesModel(name: string, value: string): boolean {
  const pattern = value.trim()
  return (
    !pattern ||
    (isModelPattern(pattern) &&
      (pattern.endsWith('*') ? name.startsWith(pattern.slice(0, -1)) : name === pattern))
  )
}
