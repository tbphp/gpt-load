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

function collectDynamicRoots(manifest, roots, boundaryEntries) {
  const visitedEntries = new Set()
  const dynamicRoots = new Set()

  function visit(key) {
    if (visitedEntries.has(key) || boundaryEntries.has(key)) return
    const entry = manifest[key]
    if (!entry) throw new Error(`bundle manifest entry not found: ${key}`)
    visitedEntries.add(key)
    for (const dependency of entry.imports ?? []) visit(dependency)
    for (const dependency of entry.dynamicImports ?? []) {
      dynamicRoots.add(dependency)
      visit(dependency)
    }
  }

  for (const root of roots) visit(root)
  return [...dynamicRoots].sort()
}

function difference(values, excluded) {
  const excludedValues = new Set(excluded)
  return values.filter((value) => !excludedValues.has(value))
}

export function collectRouteAssets(manifest, initialRoots, lazyOriginRoots) {
  const initial = collectStaticAssets(manifest, initialRoots)
  const lazyOrigins = new Set(lazyOriginRoots)
  const boundaryEntries = new Set(
    collectStaticAssets(
      manifest,
      initialRoots.filter((root) => !lazyOrigins.has(root)),
    ).entries,
  )
  const lazyRoots = collectDynamicRoots(manifest, lazyOriginRoots, boundaryEntries)
  const lazyGraph = collectStaticAssets(manifest, lazyRoots)

  return {
    initial,
    lazy: {
      roots: lazyRoots,
      entries: difference(lazyGraph.entries, initial.entries),
      javascript: difference(lazyGraph.javascript, initial.javascript),
      css: difference(lazyGraph.css, initial.css),
    },
  }
}

export function findUntrackedRouteSources(manifest, trackedSources) {
  const tracked = new Set(trackedSources)
  return Object.entries(manifest)
    .filter(([, entry]) => entry.isDynamicEntry === true)
    .map(([key, entry]) => entry.src ?? key)
    .filter((source) => /^src\/features\/.+View\.vue$/.test(source) && !tracked.has(source))
    .sort()
}

export function collectDuplicateLargeAssets(assets, minimumRawBytes) {
  const byDigest = new Map()
  for (const asset of assets) {
    if (asset.raw < minimumRawBytes) continue
    const paths = byDigest.get(asset.sha256) ?? []
    paths.push(asset.path)
    byDigest.set(asset.sha256, paths)
  }
  return [...byDigest.values()]
    .filter((paths) => paths.length > 1)
    .map((paths) => paths.sort())
    .sort((left, right) => left[0].localeCompare(right[0]))
}

function regressionCeiling(baseline, policy) {
  const allowance = Math.min(Math.ceil(baseline * policy.relative), policy.absolute_bytes)
  return baseline + allowance
}

export function collectRouteBudgetFailures(routes, lock) {
  const failures = []
  const actualNames = Object.keys(routes).sort()
  const lockedNames = Object.keys(lock.routes ?? {}).sort()

  for (const name of actualNames.filter((candidate) => !lockedNames.includes(candidate))) {
    failures.push(`${name} route is missing from bundle budget lock`)
  }
  for (const name of lockedNames.filter((candidate) => !actualNames.includes(candidate))) {
    failures.push(`${name} locked route is missing from bundle report`)
  }

  for (const name of actualNames.filter((candidate) => lockedNames.includes(candidate))) {
    for (const phase of ['initial', 'lazy']) {
      for (const assetType of ['javascript', 'css']) {
        for (const encoding of ['gzip', 'brotli']) {
          const actual = routes[name][phase].totals[assetType][encoding]
          const baseline = lock.routes[name]?.[phase]?.[assetType]?.[encoding]
          if (!Number.isSafeInteger(baseline) || baseline < 0) {
            failures.push(`${name} ${phase} ${assetType} ${encoding} lock is invalid`)
            continue
          }
          const ceiling = regressionCeiling(baseline, lock.regression_policy)
          if (actual > ceiling) {
            failures.push(
              `${name} ${phase} ${assetType} ${encoding} ${actual} > regression ceiling ${ceiling}`,
            )
          }
        }
      }
    }
  }
  return failures
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
      view: 'src/features/home/HomeView.vue',
      messages: [],
    },
    Login: {
      view: 'src/features/auth/LoginView.vue',
      messages: [],
    },
    Import: {
      view: 'src/features/import/ImportView.vue',
      messages: ['src/i18n/locales/en-US/import.ts'],
    },
    GroupDetail: {
      view: 'src/features/groups/GroupDetailView.vue',
      messages: ['src/i18n/locales/en-US/group.ts', 'src/i18n/locales/en-US/import.ts'],
    },
    AccessKeys: {
      view: 'src/features/access-keys/AccessKeysView.vue',
      messages: ['src/i18n/locales/en-US/access-keys.ts'],
    },
    Usage: {
      view: 'src/features/monitor/MonitorView.vue',
      messages: ['src/i18n/locales/en-US/monitor.ts'],
    },
    Settings: {
      view: 'src/features/settings/SettingsView.vue',
      messages: [
        'src/i18n/locales/en-US/settings.ts',
        'src/i18n/locales/en-US/model-prices.ts',
        'src/i18n/locales/en-US/import.ts',
      ],
    },
    ModelPrices: {
      view: 'src/features/model-prices/ModelPricesView.vue',
      messages: ['src/i18n/locales/en-US/model-prices.ts'],
    },
    NotFound: {
      view: 'src/features/not-found/NotFoundView.vue',
      messages: [],
    },
  }
  const assetMetrics = new Map()
  async function metric(path) {
    if (!assetMetrics.has(path)) {
      const bytes = await readFile(join(distRoot, path))
      assetMetrics.set(path, {
        path,
        sha256: createHash('sha256').update(bytes).digest('hex'),
        ...canonicalCompress(bytes),
      })
    }
    return assetMetrics.get(path)
  }

  const routes = {}
  for (const [name, definition] of Object.entries(routeDefinitions)) {
    const view = findManifestKey(manifest, definition.view)
    const roots = [
      entry,
      core,
      view,
      ...definition.messages.map((source) => findManifestKey(manifest, source)),
    ]
    const graph = collectRouteAssets(manifest, roots, [view])
    const initialJavaScript = await Promise.all(graph.initial.javascript.map(metric))
    const initialCSS = await Promise.all(graph.initial.css.map(metric))
    const lazyJavaScript = await Promise.all(graph.lazy.javascript.map(metric))
    const lazyCSS = await Promise.all(graph.lazy.css.map(metric))
    routes[name] = {
      sources: { view: definition.view, messages: definition.messages },
      initial: {
        roots,
        entries: graph.initial.entries,
        assets: { javascript: initialJavaScript, css: initialCSS },
        totals: {
          javascript: sumAssets(initialJavaScript),
          css: sumAssets(initialCSS),
        },
      },
      lazy: {
        roots: graph.lazy.roots,
        entries: graph.lazy.entries,
        assets: { javascript: lazyJavaScript, css: lazyCSS },
        totals: {
          javascript: sumAssets(lazyJavaScript),
          css: sumAssets(lazyCSS),
        },
      },
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
  const allRuntimeAssets = [
    ...new Set(
      Object.values(manifest).flatMap((manifestEntry) => [
        ...(isJavaScript(manifestEntry.file) || isCSS(manifestEntry.file)
          ? [manifestEntry.file]
          : []),
        ...(manifestEntry.css ?? []).filter(isCSS),
      ]),
    ),
  ].sort()
  const allRuntimeMetrics = await Promise.all(allRuntimeAssets.map(metric))
  const untrackedRouteSources = findUntrackedRouteSources(
    manifest,
    Object.values(routeDefinitions).map(({ view }) => view),
  )
  const duplicateLargeAssets = collectDuplicateLargeAssets(allRuntimeMetrics, 10 * 1024)
  const failures = []
  for (const [name, route] of Object.entries(routes)) {
    const totals = route.initial.totals
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
  for (const source of untrackedRouteSources) {
    failures.push(`untracked route chunk: ${source}`)
  }
  for (const paths of duplicateLargeAssets) {
    failures.push(`duplicate large output assets: ${paths.join(', ')}`)
  }

  const lockPath = join(webRoot, 'bundle-budget.lock.json')
  let lockSource = null
  let budgetLock = null
  try {
    lockSource = await readFile(lockPath, 'utf8')
    budgetLock = JSON.parse(lockSource)
  } catch (error) {
    failures.push(
      `bundle budget lock unavailable: ${error instanceof Error ? error.message : String(error)}`,
    )
  }
  if (budgetLock) {
    if (budgetLock.schema_version !== 1) {
      failures.push(`bundle budget lock schema ${budgetLock.schema_version} is unsupported`)
    } else {
      failures.push(...collectRouteBudgetFailures(routes, budgetLock))
    }
  }

  const report = {
    schema_version: 2,
    runtime: actualRuntime,
    compression: compressionOptions,
    manifest_sha256: createHash('sha256').update(manifestSource).digest('hex'),
    budget_lock_sha256:
      lockSource === null ? null : createHash('sha256').update(lockSource).digest('hex'),
    routes,
    lazy_javascript: lazyMetrics,
    lazy_css: lazyCSSMetrics,
    untracked_route_sources: untrackedRouteSources,
    duplicate_large_assets: duplicateLargeAssets,
    budgets: {
      initial_js_gzip: 161_678,
      initial_js_brotli: 135_125,
      initial_css_gzip: 15_946,
      initial_css_brotli: 13_921,
      lazy_chunk_gzip: 75 * 1024,
      duplicate_asset_raw: 10 * 1024,
      regression: budgetLock?.regression_policy ?? null,
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
        Object.entries(routes).map(([name, route]) => [
          name,
          {
            initial: route.initial.totals,
            lazy: route.lazy.totals,
          },
        ]),
      ),
      failures,
    })}\n`,
  )
  if (failures.length > 0) throw new Error('bundle budget exceeded')
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await run()
}
