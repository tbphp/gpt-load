import manifest from '../testdata/bundle-manifest/graph.json'
import {
  canonicalCompress,
  collectLazyCSSAssets,
  collectStaticAssets,
  compressionOptions,
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
})
