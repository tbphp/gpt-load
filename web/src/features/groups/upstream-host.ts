export function normalizeUpstreamHost(upstreamUrl: string): string {
  try {
    return new URL(upstreamUrl).host
  } catch {
    return upstreamUrl
  }
}
