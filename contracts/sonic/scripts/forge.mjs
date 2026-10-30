// SPDX-License-Identifier: GPL-3.0-or-later
import { spawn } from 'node:child_process';
import { createRequire } from 'node:module';
import { constants as systemConstants } from 'node:os';

const NODE_X64_ARCHITECTURE_NAME = 'x64';
const FOUNDRY_AMD64_ARCHITECTURE_NAME = 'amd64';
const WINDOWS_PLATFORM_NAME = 'win32';
const UNIX_EXECUTABLE_NAME = 'forge';
const WINDOWS_EXECUTABLE_NAME = 'forge.exe';
const PACKAGE_BINARY_PATH_SEGMENT = '/bin/';
const SCRIPT_ARGUMENT_OFFSET = 2;
const FAILURE_EXIT_CODE = 1;
const SIGNAL_EXIT_CODE_OFFSET = 128;
const INHERITED_STDIO_MODE = 'inherit';
const CHILD_ERROR_EVENT_NAME = 'error';
const CHILD_EXIT_EVENT_NAME = 'exit';
const FORWARDED_SIGNAL_NAMES = ['SIGINT', 'SIGTERM'];
const UNSUPPORTED_PLATFORM_ERROR_MESSAGE = 'No pinned Forge binary supports this platform and architecture.';
const MISSING_BINARY_ERROR_MESSAGE = 'Pinned Forge binary is unavailable. Run npm ci with optional dependencies enabled.';
const LAUNCH_ERROR_MESSAGE = 'Unable to launch the local Forge binary.';
const PACKAGE_NAMES_BY_PLATFORM = {
  darwin: {
    arm64: '@foundry-rs/forge-darwin-arm64',
    amd64: '@foundry-rs/forge-darwin-amd64',
  },
  linux: {
    arm64: '@foundry-rs/forge-linux-arm64',
    amd64: '@foundry-rs/forge-linux-amd64',
  },
  win32: {
    amd64: '@foundry-rs/forge-win32-amd64',
  },
};
const REQUIRE_FROM_SCRIPT = createRequire(import.meta.url);

function resolveArchitecture(options) {
  const { architecture } = options;
  if (architecture === NODE_X64_ARCHITECTURE_NAME) return FOUNDRY_AMD64_ARCHITECTURE_NAME;
  return architecture;
}

function resolvePackageName(options) {
  const { platform, architecture } = options;
  const packageName = PACKAGE_NAMES_BY_PLATFORM[platform]?.[architecture];
  if (packageName) return packageName;
  throw new Error(UNSUPPORTED_PLATFORM_ERROR_MESSAGE);
}

function resolveExecutableName(options) {
  const { platform } = options;
  if (platform === WINDOWS_PLATFORM_NAME) return WINDOWS_EXECUTABLE_NAME;
  return UNIX_EXECUTABLE_NAME;
}

function resolveBinary(options) {
  const { platform, architecture } = options;
  const packageName = resolvePackageName({ platform, architecture });
  const executableName = resolveExecutableName({ platform });
  try {
    return REQUIRE_FROM_SCRIPT.resolve(`${packageName}${PACKAGE_BINARY_PATH_SEGMENT}${executableName}`);
  } catch (cause) {
    throw new Error(MISSING_BINARY_ERROR_MESSAGE, { cause });
  }
}

function reportLaunchError(options) {
  const { error } = options;
  console.error(LAUNCH_ERROR_MESSAGE, {
    platform: process.platform,
    architecture: process.arch,
    reason: error.message,
  });
  process.exitCode = FAILURE_EXIT_CODE;
}

function resolveSignalExitCode(options) {
  const { signal } = options;
  const signalNumber = systemConstants.signals[signal];
  if (signalNumber) return SIGNAL_EXIT_CODE_OFFSET + signalNumber;
  return FAILURE_EXIT_CODE;
}

function forwardChildStatus(options) {
  const { child } = options;
  for (const signal of FORWARDED_SIGNAL_NAMES) {
    process.on(signal, () => child.kill(signal));
  }
  child.once(CHILD_ERROR_EVENT_NAME, (error) => reportLaunchError({ error }));
  child.once(CHILD_EXIT_EVENT_NAME, (code, signal) => {
    if (signal) {
      // Node ignores some signals, including SIGPIPE, so re-emission alone can exit successfully.
      process.exitCode = resolveSignalExitCode({ signal });
      process.removeAllListeners(signal);
      process.kill(process.pid, signal);
      return;
    }
    process.exitCode = code ?? FAILURE_EXIT_CODE;
  });
}

function runForge() {
  try {
    const architecture = resolveArchitecture({ architecture: process.arch });
    const binary = resolveBinary({ platform: process.platform, architecture });
    const child = spawn(binary, process.argv.slice(SCRIPT_ARGUMENT_OFFSET), { stdio: INHERITED_STDIO_MODE });
    // Foundry 1.7.1's npm wrapper does not forward a failing child's exit status.
    forwardChildStatus({ child });
  } catch (error) {
    reportLaunchError({ error });
  }
}

runForge();
