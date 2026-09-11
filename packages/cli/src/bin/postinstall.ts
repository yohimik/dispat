#!/usr/bin/env node
import { main } from '#root/lib/postinstall.js'
import { isMain } from '#root/lib/root.js'

async function runPostinstall(entry: () => Promise<number> = main): Promise<void> {
  process.exitCode = await entry()
}

if (isMain(import.meta.url)) await runPostinstall()

export { runPostinstall }
