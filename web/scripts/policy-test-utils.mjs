// policy 检查脚本与编辑器测试共享的最小加载原语。
// 只放 ≥2 处实际复用的 TS/VM 加载与 Vue 自定义渲染 harness；不引入测试框架，不读取临时路径。
import fs from 'node:fs'
import vm from 'node:vm'
import path from 'node:path'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
export const repoRoot = path.resolve(import.meta.dirname, '../..')
export const webRoot = path.join(repoRoot, 'web')
export const ts = require(path.join(webRoot, 'node_modules/typescript'))
export const Vue = require(path.join(webRoot, 'node_modules/vue'))
const { parse: parseSfc, compileScript } = require(
  path.join(webRoot, 'node_modules/vue/compiler-sfc'),
)
export { parseSfc }

export function readSource(relPath) {
  return fs.readFileSync(path.join(repoRoot, relPath), 'utf8')
}

export function transpile(source, overrides = {}) {
  return ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
      ...overrides,
    },
  }).outputText
}

function parse(source) {
  return ts.createSourceFile('policy.ts', source, ts.ScriptTarget.Latest, true)
}

// 去掉顶层 import 与 export 前缀，使声明成为 vm 上下文中的普通（全局）声明。
export function stripImports(source) {
  return (
    parse(source)
      .statements.filter((statement) => !ts.isImportDeclaration(statement))
      .map((statement) => statement.getFullText())
      .join('\n')
      .replace(/\bexport\s+/g, '') + '\n'
  )
}

// 按名字挑选顶层函数声明与变量声明，用于只加载被测实现。
export function extract(source, names) {
  return (
    parse(source)
      .statements.filter(
        (statement) =>
          (ts.isFunctionDeclaration(statement) && names.includes(statement.name?.text)) ||
          (ts.isVariableStatement(statement) &&
            statement.declarationList.declarations.some((declaration) =>
              names.includes(declaration.name.getText()),
            )),
      )
      .map((statement) => statement.getFullText())
      .join('\n')
      .replace(/\bexport\s+/g, '') + '\n'
  )
}

// 在传入的 vm 上下文（缺省新建）中以 CommonJS 目标求值；返回同一上下文以便读取全局/导出。
export function loadTsModule(source, context = {}) {
  context.console ??= console
  context.exports ??= {}
  vm.createContext(context)
  vm.runInContext(transpile(source), context)
  return context
}

// 编译并求值 SFC 的 script setup（含 inlineTemplate），requireFn 提供依赖替身。
// transform 可在编译后注入探针；extra 提供组件依赖的额外全局。返回 vm 上下文。
export function compileSfc(filename, sourceText, requireFn, { transform, extra } = {}) {
  const { descriptor } = parseSfc(sourceText, { filename })
  const compiled = compileScript(descriptor, { id: filename, inlineTemplate: true })
  const context = { console, exports: {}, require: requireFn, ...extra }
  vm.createContext(context)
  const code = transform ? transform(compiled.content) : compiled.content
  vm.runInContext(transpile(code, { esModuleInterop: true }), context)
  return context
}

// 极简自定义渲染器宿主：节点记录 props/events，供交互断言复用。
export function createHost() {
  const make = (tag) => ({
    tag,
    text: '',
    children: [],
    parent: null,
    props: {},
    events: {},
    style: {},
  })
  const host = {
    createElement: make,
    createText: (text) => Object.assign(make('#text'), { text }),
    createComment: (text) => Object.assign(make('#comment'), { text }),
    setText: (node, text) => (node.text = text),
    setElementText: (node, text) => Object.assign(node, { text, children: [] }),
    parentNode: (node) => node.parent,
    nextSibling: (node) => node.parent?.children[node.parent.children.indexOf(node) + 1] ?? null,
    patchProp: (el, key, prev, next) => {
      if (key.startsWith('on')) {
        if (typeof next === 'function') {
          el.events[key] = next
        } else {
          delete el.events[key]
        }
      } else if (key === 'style') {
        el.style = typeof next === 'object' && next !== null ? Object.assign(el.style, next) : {}
      } else {
        el.props[key] = next
      }
    },
    remove(node) {
      const at = node.parent?.children.indexOf(node) ?? -1
      if (at >= 0) node.parent.children.splice(at, 1)
      node.parent = null
    },
    insert(node, parent, anchor = null) {
      host.remove(node)
      const at = anchor ? parent.children.indexOf(anchor) : -1
      parent.children.splice(at < 0 ? parent.children.length : at, 0, node)
      node.parent = parent
    },
  }
  return host
}

export function mountComponent(component, props = {}) {
  const host = createHost()
  const root = host.createElement('root')
  const app = Vue.createRenderer(host).createApp(component, props)
  const errors = []
  app.config.warnHandler = () => {}
  app.config.errorHandler = (error) => errors.push(error)
  app.mount(root)
  return { host, root, errors }
}
