import path from 'node:path'
import { fileURLToPath } from 'node:url'

const sourceRoot = fileURLToPath(new URL('../src/', import.meta.url))
const aliases = {
  '@/': 'frontends/classic/',
  '@modern/': 'frontends/modern/',
  '@shared/': 'shared/',
}

function owner(filename) {
  const relative = path.relative(sourceRoot, filename).split(path.sep).join('/')
  if (relative.startsWith('../') || path.isAbsolute(relative)) return null
  if (relative.startsWith('frontends/classic/')) return 'classic'
  if (relative.startsWith('frontends/modern/')) return 'modern'
  if (relative.startsWith('shared/')) return 'shared'
  return 'entry'
}

function resolveLocalImport(source, filename) {
  for (const [alias, directory] of Object.entries(aliases)) {
    if (source.startsWith(alias)) {
      return path.resolve(sourceRoot, directory, source.slice(alias.length))
    }
  }
  if (source.startsWith('.')) return path.resolve(path.dirname(filename), source)
  if (source.startsWith('/src/')) return path.resolve(sourceRoot, source.slice(5))
  return path.isAbsolute(source) ? source : null
}

export default {
  meta: {
    type: 'problem',
    schema: [],
    messages: {
      boundary: '{{from}} 不能依赖 {{to}}；公共能力应放在 shared 内。',
      component:
        '新版公共组件不能依赖业务、布局、应用状态或 HTTP 层；通过 props、slots 和 events 组合。',
    },
  },
  create(context) {
    const filename = context.filename
    const from = owner(filename)
    if (!from || from === 'entry') return {}

    function check(source) {
      if (typeof source?.value !== 'string') return
      const resolved = resolveLocalImport(source.value, filename)
      if (!resolved) return
      const componentRoot = path.join(sourceRoot, 'frontends/modern/components') + path.sep
      const target = path.relative(sourceRoot, resolved).split(path.sep).join('/')
      if (
        filename.startsWith(componentRoot) &&
        /^(?:frontends\/modern\/(?:features|layouts|api|app)|shared\/http)\//u.test(target)
      ) {
        context.report({ node: source, messageId: 'component' })
        return
      }
      const to = owner(resolved)
      if (to && to !== from && to !== 'shared') {
        context.report({ node: source, messageId: 'boundary', data: { from, to } })
      }
    }

    return {
      ImportDeclaration(node) {
        check(node.source)
      },
      ExportNamedDeclaration(node) {
        check(node.source)
      },
      ExportAllDeclaration(node) {
        check(node.source)
      },
      ImportExpression(node) {
        check(node.source)
      },
      CallExpression(node) {
        if (
          node.callee.type === 'MemberExpression' &&
          node.callee.object.type === 'MetaProperty' &&
          node.callee.object.meta.name === 'import' &&
          node.callee.property.name === 'glob'
        ) {
          const source = node.arguments[0]
          if (source?.type === 'ArrayExpression') source.elements.forEach(check)
          else check(source)
        }
      },
    }
  },
}
