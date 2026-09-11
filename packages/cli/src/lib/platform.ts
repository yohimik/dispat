export const ASSET_NAMES = {
  'darwin-x64': 'dispat-darwin-amd64',
  'darwin-arm64': 'dispat-darwin-arm64',
  'linux-x64': 'dispat-linux-amd64',
  'linux-arm64': 'dispat-linux-arm64',
  'win32-x64': 'dispat-windows-amd64.exe',
  'win32-arm64': 'dispat-windows-arm64.exe'
} as const

export type PlatformKey = keyof typeof ASSET_NAMES

export function isPlatformKey(value: string): value is PlatformKey {
  return Object.hasOwn(ASSET_NAMES, value)
}

export function platformKey(platform: string = process.platform, arch: string = process.arch): PlatformKey {
  const key = `${platform}-${arch}`
  if (!isPlatformKey(key)) throw new Error(`unsupported platform: ${platform}/${arch}; @dispat/bin supports macOS, Linux, and Windows on x64 and ARM64`)
  return key
}

export function binaryName(platform: string = process.platform): string {
  return platform === 'win32' ? 'dispat-native.exe' : 'dispat-native'
}
