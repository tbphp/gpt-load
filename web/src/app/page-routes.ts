import pageRouteManifest from '../../../internal/webui/page_routes.json'

export interface PageRouteEntry {
  readonly name: string
  readonly path: string
}

interface UnknownRecord {
  readonly [key: string]: unknown
}

const staticSegmentPattern = /^[A-Za-z0-9][A-Za-z0-9._~-]*$/
const parameterSegmentPattern = /^:[A-Za-z][A-Za-z0-9_]*$/
const manifestFields = new Set(['version', 'routes'])
const routeFields = new Set(['name', 'path'])

function isRecord(value: unknown): value is UnknownRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isSharedPathPattern(path: string): boolean {
  if (path === '/') return true
  if (!path.startsWith('/') || path.endsWith('/')) return false

  return path
    .slice(1)
    .split('/')
    .every(
      (segment) =>
        parameterSegmentPattern.test(segment) ||
        (segment !== '.' && segment !== '..' && staticSegmentPattern.test(segment)),
    )
}

function unknownField(record: UnknownRecord, allowed: ReadonlySet<string>): string | undefined {
  return Object.keys(record).find((field) => !allowed.has(field))
}

function pageRouteShape(path: string): string {
  return path
    .split('/')
    .map((segment) => (parameterSegmentPattern.test(segment) ? ':' : segment))
    .join('/')
}

export function parsePageRouteManifest(value: unknown): readonly PageRouteEntry[] {
  if (!isRecord(value)) {
    throw new Error('Invalid page route manifest: must be an object')
  }
  const unexpectedManifestField = unknownField(value, manifestFields)
  if (unexpectedManifestField !== undefined) {
    throw new Error(`Invalid page route manifest: unknown field "${unexpectedManifestField}"`)
  }
  if (value.version !== 1) {
    throw new Error('Invalid page route manifest: version must be 1')
  }
  if (!Array.isArray(value.routes)) {
    throw new Error('Invalid page route manifest: routes must be an array')
  }
  if (value.routes.length === 0) {
    throw new Error('Invalid page route manifest: routes must not be empty')
  }

  const names = new Set<string>()
  const paths = new Set<string>()
  const shapes = new Set<string>()
  const entries = value.routes.map((route, index) => {
    if (!isRecord(route)) {
      throw new Error(`Invalid page route manifest: route at index ${index} must be an object`)
    }
    const unexpectedRouteField = unknownField(route, routeFields)
    if (unexpectedRouteField !== undefined) {
      throw new Error(
        `Invalid page route manifest: route at index ${index} has unknown field "${unexpectedRouteField}"`,
      )
    }
    if (
      typeof route.name !== 'string' ||
      route.name.trim() === '' ||
      route.name.trim() !== route.name
    ) {
      throw new Error(
        `Invalid page route manifest: route at index ${index} must have a non-empty name`,
      )
    }
    if (typeof route.path !== 'string' || route.path.trim() === '') {
      throw new Error(
        `Invalid page route manifest: route at index ${index} must have a non-empty path`,
      )
    }
    if (!isSharedPathPattern(route.path)) {
      throw new Error(
        `Invalid page route manifest: route "${route.name}" uses an unsupported shared path pattern`,
      )
    }
    if (names.has(route.name)) {
      throw new Error(`Invalid page route manifest: duplicate route name "${route.name}"`)
    }
    if (paths.has(route.path)) {
      throw new Error(`Invalid page route manifest: duplicate route path "${route.path}"`)
    }
    const shape = pageRouteShape(route.path)
    if (shapes.has(shape)) {
      throw new Error(`Invalid page route manifest: duplicate route shape "${shape}"`)
    }

    names.add(route.name)
    paths.add(route.path)
    shapes.add(shape)
    return Object.freeze({ name: route.name, path: route.path })
  })

  return Object.freeze(entries)
}

export const pageRouteEntries = parsePageRouteManifest(pageRouteManifest)

const pathsByName = new Map(pageRouteEntries.map((route) => [route.name, route.path]))

export function pagePath(name: string): string {
  const routePath = pathsByName.get(name)
  if (routePath === undefined) {
    throw new Error(`unknown page route "${name}"`)
  }
  return routePath
}

export function pagePathMatches(name: string, rawPath: string): boolean {
  const routePath = pathsByName.get(name)
  if (routePath === undefined) return false

  let decodedPath: string
  try {
    decodedPath = decodeURIComponent(rawPath)
  } catch {
    return false
  }
  if (routePath === '/' || decodedPath === '/') {
    return routePath === decodedPath
  }

  const routeSegments = routePath.slice(1).split('/')
  const pathSegments = decodedPath.startsWith('/') ? decodedPath.slice(1).split('/') : []
  if (routeSegments.length !== pathSegments.length) return false

  return routeSegments.every((segment, index) => {
    const pathSegment = pathSegments[index]
    return parameterSegmentPattern.test(segment)
      ? pathSegment !== undefined && pathSegment !== ''
      : segment === pathSegment
  })
}
