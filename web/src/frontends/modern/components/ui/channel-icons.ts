// 沿用经典版素材与 SVG 命名空间规则；新版独立持有资源，保持前端边界。
const rawIcons = {
  ...import.meta.glob('../../assets/clients/*.svg', {
    eager: true,
    query: '?raw',
    import: 'default',
  }),
  ...import.meta.glob('../../assets/channels/*.svg', {
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
const idsByName = new Map<string, readonly string[]>()
for (const [path, source] of Object.entries(rawIcons)) {
  const name = path
    .split('/')
    .pop()
    ?.replace(/\.svg$/u, '')
  if (name) {
    const markup = stripTitle(source)
    iconsByName.set(name, markup)
    // 静态素材的 ID 只解析一次，实例化时仍分别替换命名空间。
    idsByName.set(name, [
      ...new Set([...markup.matchAll(/\bid="([^"]+)"/gu)].map((match) => match[1])),
    ])
  }
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
  const ids = idsByName.get(icon) ?? []
  if (ids.length === 0) return markup
  let namespaced = markup
  for (const id of ids) {
    namespaced = namespaced
      .replaceAll(`id="${id}"`, `id="${instanceId}-${id}"`)
      .replaceAll(`url(#${id})`, `url(#${instanceId}-${id})`)
  }
  return namespaced
}

export function channelIconRasterURL(icon: string): string | null {
  return rasterIconsByName.get(icon) ?? null
}
