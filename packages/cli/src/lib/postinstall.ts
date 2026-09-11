#!/usr/bin/env node
'use strict'

import { install } from '#root/lib/install.js'

interface PostinstallOptions {
  installer?: typeof install
  stderr?: Pick<NodeJS.WriteStream, 'write'>
  env?: NodeJS.ProcessEnv
}

export async function main(options: PostinstallOptions = {}): Promise<number> {
  const stderr = options.stderr ?? process.stderr
  try {
    const { installed } = await (options.installer ?? install)({ log(level, message, fields) {
      if ((options.env ?? process.env).DISPAT_NPM_DEBUG || level === 'warn') {
        const detail = Object.keys(fields).length ? ` ${JSON.stringify(fields)}` : ''
        stderr.write(`dispat [${level}]: ${message}${detail}\n`)
      }
    } })
    if (installed) stderr.write('dispat [info]: installed verified native executable\n')
    return 0
  } catch (error) {
    const failure = error instanceof Error ? error : new Error(String(error))
    stderr.write(`dispat [error]: installation failed: ${failure.message}\n`)
    stderr.write('dispat: after fixing the problem, run `node node_modules/@dispat/bin/build/bin/postinstall.js` locally; for a global install run the same file under the directory printed by `npm root -g`.\n')
    if ((options.env ?? process.env).DISPAT_NPM_DEBUG) stderr.write(`${failure.stack}\n`)
    return 1
  }
}
