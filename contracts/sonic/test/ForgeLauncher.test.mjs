// SPDX-License-Identifier: GPL-3.0-or-later
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { constants as systemConstants, tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

const WINDOWS_PLATFORM_NAME = 'win32';
const NODE_X64_ARCHITECTURE_NAME = 'x64';
const FOUNDRY_AMD64_ARCHITECTURE_NAME = 'amd64';
const FORGE_TOOL_NAME = 'forge';
const WINDOWS_EXECUTABLE_NAME = 'forge.exe';
const PACKAGE_COMPONENT_SEPARATOR = '-';
const OPTIONAL_PACKAGE_PATH_COMPONENTS = ['node_modules', '@foundry-rs'];
const BINARY_DIRECTORY_NAME = 'bin';
const TEMPORARY_DIRECTORY_PREFIX = 'dispat-forge-launcher-';
const LAUNCHER_SOURCE_RELATIVE_PATH = '../scripts/forge.mjs';
const LAUNCHER_FILE_NAME = 'forge.mjs';
const RECURSIVE_DIRECTORY_FLAG = true;
const FORCE_REMOVE_FLAG = true;
const EXECUTABLE_PERMISSION_MODE = 0o755;
const OUTPUT_ENCODING_NAME = 'utf8';
const CHILD_TIMEOUT_MILLISECONDS = 10000;
const NODE_EVALUATE_ARGUMENT = '--eval';
const NODE_ARGUMENT_SEPARATOR = '--';
const FORWARDED_ARGUMENT_VALUES = ['--sentinel', 'two words', '$(literal)'];
const ARGUMENT_ECHO_SCRIPT = 'process.stdout.write(JSON.stringify(process.argv.slice(1))); process.exit(7);';
const SUCCESS_SCRIPT = 'process.exit(0);';
const SIGPIPE_SCRIPT = '#!/bin/sh\nkill -PIPE $$\n';
const EXPECTED_FAILURE_EXIT_CODE = 7;
const EXPECTED_SUCCESS_EXIT_CODE = 0;
const SIGNAL_EXIT_CODE_OFFSET = 128;
const NO_TERMINATION_SIGNAL = null;
const EMPTY_OUTPUT_TEXT = '';
const ORDINARY_PROCESS_TEST_NAME = 'launcher preserves literal arguments and ordinary exit codes';
const SIGNAL_PROCESS_TEST_NAME = 'launcher reports a SIGPIPE child as a failed process';

function resolveArchitecture(options) {
  const { architecture } = options;
  if (architecture === NODE_X64_ARCHITECTURE_NAME) return FOUNDRY_AMD64_ARCHITECTURE_NAME;
  return architecture;
}

function resolveExecutableName(options) {
  const { platform } = options;
  if (platform === WINDOWS_PLATFORM_NAME) return WINDOWS_EXECUTABLE_NAME;
  return FORGE_TOOL_NAME;
}

function createLauncherFixture(options) {
  const { context } = options;
  const directoryPath = mkdtempSync(join(tmpdir(), TEMPORARY_DIRECTORY_PREFIX));
  context.after(() => rmSync(directoryPath, { recursive: RECURSIVE_DIRECTORY_FLAG, force: FORCE_REMOVE_FLAG }));
  const architecture = resolveArchitecture({ architecture: process.arch });
  const packageName = [FORGE_TOOL_NAME, process.platform, architecture].join(PACKAGE_COMPONENT_SEPARATOR);
  const binaryDirectoryPath = join(directoryPath, ...OPTIONAL_PACKAGE_PATH_COMPONENTS, packageName, BINARY_DIRECTORY_NAME);
  mkdirSync(binaryDirectoryPath, { recursive: RECURSIVE_DIRECTORY_FLAG });
  const launcherPath = join(directoryPath, LAUNCHER_FILE_NAME);
  copyFileSync(new URL(LAUNCHER_SOURCE_RELATIVE_PATH, import.meta.url), launcherPath);
  const binaryPath = join(binaryDirectoryPath, resolveExecutableName({ platform: process.platform }));
  return { launcherPath, binaryPath };
}

function runLauncher(options) {
  const { fixture, arguments: launcherArguments } = options;
  const result = spawnSync(process.execPath, [fixture.launcherPath, ...launcherArguments], {
    encoding: OUTPUT_ENCODING_NAME,
    timeout: CHILD_TIMEOUT_MILLISECONDS,
  });
  assert.ifError(result.error);
  assert.equal(result.stderr, EMPTY_OUTPUT_TEXT);
  assert.equal(result.signal, NO_TERMINATION_SIGNAL);
  return result;
}

test(ORDINARY_PROCESS_TEST_NAME, (context) => {
  const fixture = createLauncherFixture({ context });
  // A copied Node executable exercises the real package resolution on Windows as well as POSIX.
  copyFileSync(process.execPath, fixture.binaryPath);
  const failure = runLauncher({
    fixture,
    arguments: [NODE_EVALUATE_ARGUMENT, ARGUMENT_ECHO_SCRIPT, NODE_ARGUMENT_SEPARATOR, ...FORWARDED_ARGUMENT_VALUES],
  });
  assert.equal(failure.status, EXPECTED_FAILURE_EXIT_CODE);
  assert.deepEqual(JSON.parse(failure.stdout), FORWARDED_ARGUMENT_VALUES);
  const success = runLauncher({ fixture, arguments: [NODE_EVALUATE_ARGUMENT, SUCCESS_SCRIPT] });
  assert.equal(success.status, EXPECTED_SUCCESS_EXIT_CODE);
});

test(SIGNAL_PROCESS_TEST_NAME, { skip: process.platform === WINDOWS_PLATFORM_NAME }, (context) => {
  const fixture = createLauncherFixture({ context });
  writeFileSync(fixture.binaryPath, SIGPIPE_SCRIPT, { mode: EXECUTABLE_PERMISSION_MODE });
  const result = runLauncher({ fixture, arguments: [] });
  assert.equal(result.status, SIGNAL_EXIT_CODE_OFFSET + systemConstants.signals.SIGPIPE);
});
