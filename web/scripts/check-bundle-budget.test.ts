import manifest from '../testdata/bundle-manifest/graph.json'
import {
  canonicalCompress,
  collectDuplicateLargeAssets,
  collectLazyCSSAssets,
  collectRouteAssets,
  collectRouteBudgetFailures,
  collectStaticAssets,
  compressionOptions,
  findUntrackedRouteSources,
} from './check-bundle-budget.mjs'

describe('canonical bundle graph', () => {
  it('walks only static imports, de-duplicates shared assets, and separates CSS', () => {
    const result = collectStaticAssets(manifest, ['src/main.ts', 'src/features/a.vue'])

    expect(result.entries).toEqual([
      '_a-static.js',
      '_shared.js',
      'src/features/a.vue',
      'src/main.ts',
      'src/metadata.html',
    ])
    expect(result.javascript).toEqual([
      'assets/a-static.js',
      'assets/a.js',
      'assets/main.js',
      'assets/shared.js',
    ])
    expect(result.css).toEqual(['assets/a-lazy.css', 'assets/main.css', 'assets/shared.css'])
    expect(JSON.stringify(result)).not.toContain('feature-b')
    expect(JSON.stringify({ javascript: result.javascript, css: result.css })).not.toContain('.map')
    expect(JSON.stringify({ javascript: result.javascript, css: result.css })).not.toContain(
      '.html',
    )
  })

  it('uses deterministic in-process gzip and brotli parameters', () => {
    const payload = new TextEncoder().encode('canonical bundle payload '.repeat(100))
    expect(canonicalCompress(payload)).toEqual(canonicalCompress(payload))
    expect(compressionOptions).toEqual({
      gzip: { level: 9, mtime: 0 },
      brotli: { quality: 11, mode: 'text', size_hint: true },
    })
  })

  it('reports CSS outside the initial entry graph as lazy CSS', () => {
    expect(collectLazyCSSAssets(manifest, 'src/main.ts')).toEqual([
      'assets/a-lazy.css',
      'assets/feature-b.css',
    ])
  })

  it('separates one route static graph from its descendant lazy graph', () => {
    const result = collectRouteAssets(
      manifest,
      ['src/main.ts', 'src/features/a.vue'],
      ['src/features/a.vue'],
    )

    expect(result.initial.javascript).toEqual([
      'assets/a-static.js',
      'assets/a.js',
      'assets/main.js',
      'assets/shared.js',
    ])
    expect(result.lazy.javascript).toEqual(['assets/feature-b.js'])
    expect(result.initial.css).toEqual([
      'assets/a-lazy.css',
      'assets/main.css',
      'assets/shared.css',
    ])
    expect(result.lazy.css).toEqual(['assets/feature-b.css'])
  })

  it('rejects untracked route views and duplicate large output assets', () => {
    expect(
      findUntrackedRouteSources(
        {
          'src/features/known/KnownView.vue': {
            file: 'assets/known.js',
            src: 'src/features/known/KnownView.vue',
            isDynamicEntry: true,
          },
          'src/features/orphan/OrphanView.vue': {
            file: 'assets/orphan.js',
            src: 'src/features/orphan/OrphanView.vue',
            isDynamicEntry: true,
          },
          'src/features/known/Child.vue': {
            file: 'assets/child.js',
            src: 'src/features/known/Child.vue',
            isDynamicEntry: true,
          },
        },
        ['src/features/known/KnownView.vue'],
      ),
    ).toEqual(['src/features/orphan/OrphanView.vue'])

    expect(
      collectDuplicateLargeAssets(
        [
          { path: 'assets/a.js', raw: 20_000, sha256: 'same' },
          { path: 'assets/b.js', raw: 20_000, sha256: 'same' },
          { path: 'assets/small-a.js', raw: 10, sha256: 'small' },
          { path: 'assets/small-b.js', raw: 10, sha256: 'small' },
        ],
        10_240,
      ),
    ).toEqual([['assets/a.js', 'assets/b.js']])
  })

  it('applies the smaller five-percent or ten-KiB regression allowance to every route axis', () => {
    const totals = (gzip: number) => ({
      javascript: { raw: gzip, gzip, brotli: gzip },
      css: { raw: gzip, gzip, brotli: gzip },
    })
    const routes = {
      Home: {
        initial: { totals: totals(1_051) },
        lazy: { totals: totals(0) },
      },
    }
    const lock = {
      regression_policy: { relative: 0.05, absolute_bytes: 10_240 },
      routes: {
        Home: {
          initial: {
            javascript: { gzip: 1_000, brotli: 1_000 },
            css: { gzip: 1_000, brotli: 1_000 },
          },
          lazy: {
            javascript: { gzip: 0, brotli: 0 },
            css: { gzip: 0, brotli: 0 },
          },
        },
      },
    }

    expect(collectRouteBudgetFailures(routes, lock)).toEqual([
      'Home initial javascript gzip 1051 > regression ceiling 1050',
      'Home initial javascript brotli 1051 > regression ceiling 1050',
      'Home initial css gzip 1051 > regression ceiling 1050',
      'Home initial css brotli 1051 > regression ceiling 1050',
    ])
  })
})
