const maxImportBytes = 32 * 1024 * 1024
const maxImportCredentials = 5000

export interface APIKeyFileImport {
  credentials: string
  count: number
}

export class APIKeyFileImportError extends Error {
  constructor(readonly code: 'empty' | 'too_large' | 'too_many' | 'invalid_json' | 'read_failed') {
    super(code)
  }
}

export async function readAPIKeyCredentialFiles(files: readonly File[]): Promise<APIKeyFileImport> {
  if (files.reduce((size, file) => size + file.size, 0) > maxImportBytes)
    throw new APIKeyFileImportError('too_large')

  const contents: string[] = []
  for (const file of files) {
    let content: string
    try {
      content = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer()).trim()
    } catch {
      throw new APIKeyFileImportError('read_failed')
    }
    if (!content) continue
    let parsed: unknown
    try {
      parsed = JSON.parse(content)
    } catch {
      if (/\.json$/iu.test(file.name)) throw new APIKeyFileImportError('invalid_json')
    }
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      // 每份格式化 JSON 压成一行，合并后仍沿用现有文本导入合同。
      content = JSON.stringify(parsed)
    } else if (/\.json$/iu.test(file.name)) {
      throw new APIKeyFileImportError('invalid_json')
    }
    contents.push(content)
  }

  const credentials = contents.join('\n')
  const count = credentials.split('\n').filter((line) => line.trim()).length
  if (!count) throw new APIKeyFileImportError('empty')
  if (count > maxImportCredentials) throw new APIKeyFileImportError('too_many')
  if (new TextEncoder().encode(JSON.stringify({ credentials })).length > maxImportBytes)
    throw new APIKeyFileImportError('too_large')
  return { credentials, count }
}
