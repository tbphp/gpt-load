import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { brotliCompressSync, constants as zlibConstants, gzipSync } from 'node:zlib'

export const canonicalRuntime = {
  node: '24.18.0',
  zlib: '1.3.1-e00f703',
  brotli: '1.2.0',
  vite: '8.1.5',
}
export const compressionOptions = {
  gzip: { level: 9, mtime: 0 },
  brotli: { quality: 11, mode: 'text', size_hint: true },
}

function isJavaScript(path) {
  return path.endsWith('.js') || path.endsWith('.mjs')
}

function isCSS(path) {
  return path.endsWith('.css')
}

export function collectStaticAssets(manifest, roots) {
  const visitedEntries = new Set()
  const javascript = new Set()
  const css = new Set()

  function visit(key) {
    if (visitedEntries.has(key)) return
    const entry = manifest[key]
    if (!entry) throw new Error(`bundle manifest entry not found: ${key}`)
    visitedEntries.add(key)
    if (isJavaScript(entry.file)) javascript.add(entry.file)
    if (isCSS(entry.file)) css.add(entry.file)
    for (const asset of entry.css ?? []) {
      if (isCSS(asset)) css.add(asset)
    }
    for (const dependency of entry.imports ?? []) visit(dependency)
  }

  for (const root of roots) visit(root)
  return {
    entries: [...visitedEntries].sort(),
    javascript: [...javascript].sort(),
    css: [...css].sort(),
  }
}

export function collectLazyCSSAssets(manifest, entry) {
  const initialCSS = new Set(collectStaticAssets(manifest, [entry]).css)
  return [
    ...new Set(
      Object.values(manifest).flatMap((manifestEntry) => [
        ...(isCSS(manifestEntry.file) ? [manifestEntry.file] : []),
        ...(manifestEntry.css ?? []).filter(isCSS),
      ]),
    ),
  ]
    .filter((asset) => !initialCSS.has(asset))
    .sort()
}

export function canonicalCompress(bytes) {
  const gzip = gzipSync(bytes, { level: compressionOptions.gzip.level, mtime: 0 })
  const brotli = brotliCompressSync(bytes, {
    params: {
      [zlibConstants.BROTLI_PARAM_MODE]: zlibConstants.BROTLI_MODE_TEXT,
      [zlibConstants.BROTLI_PARAM_QUALITY]: compressionOptions.brotli.quality,
      [zlibConstants.BROTLI_PARAM_SIZE_HINT]: bytes.length,
    },
  })
  return { raw: bytes.length, gzip: gzip.length, brotli: brotli.length }
}

function findManifestKey(manifest, source) {
  const key = Object.keys(manifest).find(
    (candidate) => candidate === source || manifest[candidate].src === source,
  )
  if (!key) throw new Error(`bundle manifest source not found: ${source}`)
  return key
}

function sumAssets(assets) {
  return assets.reduce(
    (total, asset) => ({
      raw: total.raw + asset.raw,
      gzip: total.gzip + asset.gzip,
      brotli: total.brotli + asset.brotli,
    }),
    { raw: 0, gzip: 0, brotli: 0 },
  )
}

async function run() {
  const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
  const repositoryRoot = resolve(webRoot, '..')
  const distRoot = join(repositoryRoot, 'internal/webui/dist')
  const manifestPath = join(distRoot, '.vite/manifest.json')
  const manifestSource = await readFile(manifestPath, 'utf8')
  const manifest = JSON.parse(manifestSource)
  const packageJSON = JSON.parse(await readFile(join(webRoot, 'package.json'), 'utf8'))
  const actualRuntime = {
    node: process.versions.node,
    zlib: process.versions.zlib,
    brotli: process.versions.brotli,
    vite: packageJSON.devDependencies.vite,
  }
  if (JSON.stringify(actualRuntime) !== JSON.stringify(canonicalRuntime)) {
    throw new Error(`bundle runtime mismatch: ${JSON.stringify(actualRuntime)}`)
  }

  const entry = findManifestKey(manifest, 'index.html')
  const core = findManifestKey(manifest, 'src/i18n/locales/en-US/core.ts')
  const routeDefinitions = {
    Home: {
      sources: ['src/features/home/HomeView.vue'],
    },
    Login: {
      sources: ['src/features/auth/LoginView.vue'],
    },
    AccessKeys: {
      sources: [
        'src/features/access-keys/AccessKeysView.vue',
        'src/i18n/locales/en-US/access-keys.ts',
      ],
    },
    Usage: {
      sources: ['src/features/monitor/MonitorView.vue', 'src/i18n/locales/en-US/monitor.ts'],
    },
    ModelPrices: {
      sources: [
        'src/features/model-prices/ModelPricesView.vue',
        'src/i18n/locales/en-US/model-prices.ts',
      ],
    },
  }
  const assetMetrics = new Map()
  async function metric(path) {
    if (!assetMetrics.has(path)) {
      const bytes = await readFile(join(distRoot, path))
      assetMetrics.set(path, { path, ...canonicalCompress(bytes) })
    }
    return assetMetrics.get(path)
  }

  const routes = {}
  for (const [name, definition] of Object.entries(routeDefinitions)) {
    const roots = [
      entry,
      core,
      ...definition.sources.map((source) => findManifestKey(manifest, source)),
    ]
    const graph = collectStaticAssets(manifest, roots)
    const javascript = await Promise.all(graph.javascript.map(metric))
    const css = await Promise.all(graph.css.map(metric))
    routes[name] = {
      roots,
      entries: graph.entries,
      assets: { javascript, css },
      totals: { javascript: sumAssets(javascript), css: sumAssets(css) },
    }
  }

  const baseAssets = new Set(collectStaticAssets(manifest, [entry]).javascript)
  const lazyJavaScript = [
    ...new Set(
      Object.values(manifest)
        .map(({ file }) => file)
        .filter((file) => isJavaScript(file) && !baseAssets.has(file)),
    ),
  ].sort()
  const lazyMetrics = await Promise.all(lazyJavaScript.map(metric))
  const lazyCSSMetrics = await Promise.all(collectLazyCSSAssets(manifest, entry).map(metric))
  const failures = []
  for (const name of ['Home', 'Login']) {
    const totals = routes[name].totals
    if (totals.javascript.gzip > 161_678) {
      failures.push(`${name} initial JS gzip ${totals.javascript.gzip} > 161678`)
    }
    if (totals.javascript.brotli > 135_125) {
      failures.push(`${name} initial JS brotli ${totals.javascript.brotli} > 135125`)
    }
    if (totals.css.gzip > 15_946) {
      failures.push(`${name} initial CSS gzip ${totals.css.gzip} > 15946`)
    }
    if (totals.css.brotli > 13_921) {
      failures.push(`${name} initial CSS brotli ${totals.css.brotli} > 13921`)
    }
  }
  for (const asset of lazyMetrics) {
    if (asset.gzip > 75 * 1024) {
      failures.push(`${asset.path} lazy JS gzip ${asset.gzip} > ${75 * 1024}`)
    }
  }

  const report = {
    schema_version: 1,
    runtime: actualRuntime,
    compression: compressionOptions,
    manifest_sha256: createHash('sha256').update(manifestSource).digest('hex'),
    routes,
    lazy_javascript: lazyMetrics,
    lazy_css: lazyCSSMetrics,
    budgets: {
      initial_js_gzip: 161_678,
      initial_js_brotli: 135_125,
      initial_css_gzip: 15_946,
      initial_css_brotli: 13_921,
      lazy_chunk_gzip: 75 * 1024,
    },
    failures,
  }
  const reportPath = join(webRoot, 'test-results/bundle-budget.json')
  await mkdir(dirname(reportPath), { recursive: true })
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`)
  process.stdout.write(
    `${JSON.stringify({
      report: reportPath,
      routes: Object.fromEntries(
        Object.entries(routes).map(([name, route]) => [name, route.totals]),
      ),
      failures,
    })}\n`,
  )
  if (failures.length > 0) throw new Error('bundle budget exceeded')
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await run()
}
