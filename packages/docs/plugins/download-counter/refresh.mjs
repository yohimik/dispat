import {spawnSync} from 'node:child_process';
import {mkdirSync} from 'node:fs';
import {fileURLToPath} from 'node:url';

const site = fileURLToPath(new URL('../../', import.meta.url));
const output = fileURLToPath(new URL('../../data/download-counter/', import.meta.url));
let token = process.env.GITHUB_TOKEN;
if (!token) {
  const auth = spawnSync('gh', ['auth', 'token'], {encoding: 'utf8'});
  if (auth.status === 0) token = auth.stdout.trim();
}
if (!token) {
  console.error('Set GITHUB_TOKEN or sign in with gh auth login, then run downloads:refresh again. The token stays in the collector process.');
  process.exit(1);
}
mkdirSync(output, {recursive: true});
const result = spawnSync('docker', [
  'run', '--rm', '-e', 'GITHUB_TOKEN', '-e', 'GOWORK=off',
  '-v', `${site}download-counter:/src:ro`, '-v', `${output}:/out`, '-w', '/src',
  'golang:1.26-bookworm', 'go', 'run', '.', '-output', '/out/downloads.json',
], {env: {...process.env, GITHUB_TOKEN: token}, stdio: 'inherit'});
if (result.error) console.error(`Cannot run the collector: ${result.error.message}`);
if (result.status !== 0) process.exit(result.status ?? 1);
console.info('Local snapshot ready. pnpm docs:start serves it at /downloads.json; production builds exclude it.');
