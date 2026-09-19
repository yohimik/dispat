import {existsSync, readFileSync} from 'node:fs';

// A source checkout builds its own CLI. A distributed package must always run
// the verified installer, including when its release metadata is missing.
const source = new URL('./src/bin/postinstall.ts', import.meta.url);
const workspace = new URL('../../pnpm-workspace.yaml', import.meta.url);
const manifest = new URL('../../package.json', import.meta.url);

// Only this repository's own manifest says a checkout is the source workspace.
// A manifest that cannot be read or cannot be parsed says nothing, and nothing
// is not a reason to skip the installer: every answer but the one name here is
// "install", which is the answer that leaves a working CLI behind.
function isMonorepoManifest(file) {
  try {
    return JSON.parse(readFileSync(file, 'utf8')).name === 'dispat-monorepo';
  } catch {
    return false;
  }
}

const isSourceWorkspace = existsSync(source) && existsSync(workspace)
  && isMonorepoManifest(manifest);

if (!isSourceWorkspace) {
  const {runPostinstall} = await import('./build/bin/postinstall.js');
  await runPostinstall();
}
