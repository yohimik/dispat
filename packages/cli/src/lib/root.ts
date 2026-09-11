import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { realpathSync } from 'node:fs'

export const PACKAGE_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')

export function isMain(metaUrl: string): boolean {
  if (!process.argv[1]) return false
  try { return realpathSync(fileURLToPath(metaUrl)) === realpathSync(process.argv[1]) }
  catch { return metaUrl === pathToFileURL(process.argv[1]).href }
}
