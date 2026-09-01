// 渠道（上游服务商）与客户端（下游接入方）的图标来源相同、渲染方式相同，
// 只是分目录存放，所以合并成一张注册表。两边不得出现同名文件：
// 需要复用同一个品牌时（Claude Code 用 claude），直接引用渠道那份。
const rawIcons = {
  ...import.meta.glob('../../assets/channels/*.svg', {
    eager: true,
    query: '?raw',
    import: 'default',
  }),
  ...import.meta.glob('../../assets/clients/*.svg', {
    eager: true,
    query: '?raw',
    import: 'default',
  }),
}

const rasterIcons = import.meta.glob<string>('../../assets/channels/*.webp', {
  eager: true,
  query: '?url',
  import: 'default',
})

// Keep the whole <svg> tag, not just its children: presentation attributes
// such as fill="currentColor" often live on the root element, not on each
// <path>, so extracting only the inner markup silently drops them.
function stripTitle(markup: string): string {
  return markup.replace(/<title>.*?<\/title>/su, '')
}

const iconsByName = new Map<string, string>()
for (const [path, source] of Object.entries(rawIcons)) {
  const name = path
    .split('/')
    .pop()
    ?.replace(/\.svg$/u, '')
  if (name) iconsByName.set(name, stripTitle(source))
}

const rasterIconsByName = new Map<string, string>()
for (const [path, source] of Object.entries(rasterIcons)) {
  const name = path
    .split('/')
    .pop()
    ?.replace(/\.webp$/u, '')
  if (name) rasterIconsByName.set(name, source)
}

// SVG element IDs (gradient defs, etc.) must be unique per rendered instance,
// otherwise two chips for the same channel collide and one can lose its fill
// when the other unmounts.
export function namespacedChannelIconMarkup(icon: string, instanceId: string): string | null {
  const markup = iconsByName.get(icon)
  if (!markup) return null
  const ids = new Set<string>()
  const idPattern = /\bid="([^"]+)"/gu
  for (const match of markup.matchAll(idPattern)) ids.add(match[1])
  if (ids.size === 0) return markup
  let namespaced = markup
  for (const id of ids) {
    const escaped = id.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&')
    namespaced = namespaced
      .replaceAll(new RegExp(`id="${escaped}"`, 'gu'), `id="${instanceId}-${id}"`)
      .replaceAll(new RegExp(`url\\(#${escaped}\\)`, 'gu'), `url(#${instanceId}-${id})`)
  }
  return namespaced
}

export function channelIconRasterURL(icon: string): string | null {
  return rasterIconsByName.get(icon) ?? null
}

let instanceCounter = 0
export function nextChannelIconInstanceId(): string {
  instanceCounter += 1
  return `channel-icon-${instanceCounter}`
}
