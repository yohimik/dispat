#!/usr/bin/env node
'use strict'

import fs from 'node:fs'
import path from 'node:path'
import { readFileSync } from 'node:fs'
import { validateMetadata } from '#root/lib/install.js'
import { PACKAGE_ROOT, isMain } from '#root/lib/root.js'

function verify(root = PACKAGE_ROOT) {
  const pkg = JSON.parse(readFileSync(path.join(root, 'package.json'), 'utf8'))
  const release = JSON.parse(readFileSync(path.join(root, 'release.json'), 'utf8'))
  if (pkg.version.split('.').slice(0, 2).join('.') !== release.version.split('.').slice(0, 2).join('.')) {
    throw new Error(`npm ${pkg.version} and binary ${release.version} must share the cli group's major/minor`)
  }
  for (const key of ['darwin-x64', 'darwin-arm64', 'linux-x64', 'linux-arm64', 'win32-x64', 'win32-arm64']) validateMetadata(release, key)
  for (const file of ['build/bin/dispat.js', 'build/bin/postinstall.js', 'README.md', 'LICENSE']) {
    if (!fs.existsSync(path.join(root, file))) throw new Error(`package is missing ${file}`)
  }
}
if (isMain(import.meta.url)) verify()
export { verify }
