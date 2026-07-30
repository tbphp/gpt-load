import pageRouteManifest from '../../../internal/webui/page_routes.json'

import {
  pagePath,
  pagePathMatches,
  pageRouteEntries,
  parsePageRouteManifest,
  type PageRouteEntry,
} from './page-routes'

const expectedRoutes = [
  { name: 'home', path: '/' },
  { name: 'login', path: '/login' },
  { name: 'import', path: '/import' },
  { name: 'group-detail', path: '/groups/:id' },
  { name: 'access-keys', path: '/access-keys' },
  { name: 'monitor', path: '/monitor' },
  { name: 'settings', path: '/settings' },
  { name: 'model-prices', path: '/settings/model-prices' },
]

describe('page route manifest', () => {
  it('declares the current explicit management pages as the version 1 contract', () => {
    expect(pageRouteManifest).toEqual({
      version: 1,
      routes: expectedRoutes,
    })
  })

  it('parses and freezes a valid hand-written route contract', () => {
    const routes = parsePageRouteManifest({
      version: 1,
      routes: [
        { name: 'dashboard', path: '/' },
        { name: 'item-detail', path: '/items/:item_id' },
      ],
    })

    expect(routes).toEqual([
      { name: 'dashboard', path: '/' },
      { name: 'item-detail', path: '/items/:item_id' },
    ])
    expect(Object.isFrozen(routes)).toBe(true)
    expect(Object.isFrozen(routes[0])).toBe(true)
  })

  it.each([
    ['missing object', null, /must be an object/],
    ['unsupported version', { version: 2, routes: [] }, /version must be 1/],
    ['missing routes', { version: 1 }, /routes must be an array/],
    ['empty routes', { version: 1, routes: [] }, /routes must not be empty/],
    [
      'unknown manifest field',
      { version: 1, routes: [{ name: 'home', path: '/' }], unexpected: true },
      /unknown field "unexpected"/,
    ],
    ['non-object entry', { version: 1, routes: [null] }, /route at index 0 must be an object/],
    [
      'unknown route field',
      { version: 1, routes: [{ name: 'home', path: '/', unexpected: true }] },
      /route at index 0 has unknown field "unexpected"/,
    ],
    [
      'empty name',
      { version: 1, routes: [{ name: '  ', path: '/valid' }] },
      /route at index 0 must have a non-empty name/,
    ],
    [
      'name with surrounding whitespace',
      { version: 1, routes: [{ name: ' invalid ', path: '/valid' }] },
      /route at index 0 must have a non-empty name/,
    ],
    [
      'empty path',
      { version: 1, routes: [{ name: 'valid', path: '' }] },
      /route at index 0 must have a non-empty path/,
    ],
  ])('rejects %s', (_name, manifest, expectedError) => {
    expect(() => parsePageRouteManifest(manifest)).toThrow(expectedError as RegExp)
  })

  it.each([
    'settings',
    '/settings/',
    '/groups//keys',
    '/groups/group-:id',
    '/groups/:id.json',
    '/groups/:id(\\d+)',
    '/files/*path',
    '/monitor?tab=logs',
    '/monitor#usage',
    '/groups/:',
    '/groups/:9id',
    '/groups/:_id',
    '/groups/..',
    '/.hidden',
    '/-draft',
    '/_private',
  ])('rejects the non-shared route pattern %s', (path) => {
    expect(() =>
      parsePageRouteManifest({
        version: 1,
        routes: [{ name: 'invalid', path }],
      }),
    ).toThrow(/uses an unsupported shared path pattern/)
  })

  it('rejects duplicate route names', () => {
    expect(() =>
      parsePageRouteManifest({
        version: 1,
        routes: [
          { name: 'duplicate', path: '/first' },
          { name: 'duplicate', path: '/second' },
        ],
      }),
    ).toThrow(/duplicate route name "duplicate"/)
  })

  it('rejects duplicate route paths', () => {
    expect(() =>
      parsePageRouteManifest({
        version: 1,
        routes: [
          { name: 'first', path: '/duplicate' },
          { name: 'second', path: '/duplicate' },
        ],
      }),
    ).toThrow(/duplicate route path "\/duplicate"/)
  })

  it('rejects routes with the same parameterized shape', () => {
    expect(() =>
      parsePageRouteManifest({
        version: 1,
        routes: [
          { name: 'group-by-id', path: '/groups/:id' },
          { name: 'group-by-slug', path: '/groups/:slug' },
        ],
      }),
    ).toThrow(/duplicate route shape "\/groups\/:"/)
  })

  it('exports the validated repository routes and resolves paths by name', () => {
    expect(pageRouteEntries).toEqual(expectedRoutes)
    expect(pagePath('home')).toBe('/')
    expect(pagePath('group-detail')).toBe('/groups/:id')
    expect(() => pagePath('not-found')).toThrow(/unknown page route "not-found"/)
  })

  it.each([
    ['settings', '/settings', true],
    ['settings', '/SETTINGS', false],
    ['settings', '/settings/', false],
    ['group-detail', '/groups/42', true],
    ['group-detail', '/groups/a%2Fb', false],
    ['group-detail', '/groups/a%252Fb', true],
    ['missing', '/missing', false],
  ])('matches %s against canonical decoded path %s', (name, path, expected) => {
    expect(pagePathMatches(name, path)).toBe(expected)
  })

  it('exposes immutable route entry values', () => {
    const first = pageRouteEntries[0] as PageRouteEntry

    expect(Object.isFrozen(pageRouteEntries)).toBe(true)
    expect(Object.isFrozen(first)).toBe(true)
  })
})
