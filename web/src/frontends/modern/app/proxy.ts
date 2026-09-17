export function validProxyURL(value: string): boolean {
  try {
    const url = new URL(value)
    const authority = value.slice(value.indexOf('://') + 3).split('/')[0] ?? ''
    return (
      ['http:', 'socks5:'].includes(url.protocol) &&
      Boolean(url.hostname) &&
      !value.includes('?') &&
      !value.includes('#') &&
      !authority.endsWith(':') &&
      url.port !== '0' &&
      (url.protocol !== 'socks5:' || Boolean(url.port)) &&
      ['', '/'].includes(url.pathname) &&
      (!authority.includes('@') || Boolean(url.username && url.password))
    )
  } catch {
    return false
  }
}
