import {usePluginData} from '@docusaurus/useGlobalData';
import {useDocsVersion} from '@docusaurus/plugin-content-docs/client';
import Admonition from '@theme/Admonition';
import React from 'react';
import {BINARY_SIZES_PLUGIN} from '@site/plugins/binary-sizes/name';
import type {BinarySizesData} from '@site/plugins/binary-sizes/types';

function mib(bytes: number): string { return `${(bytes / 1024 / 1024).toFixed(2)} MiB`; }

export default function BinarySizes(): React.ReactElement {
  const data = usePluginData(BINARY_SIZES_PLUGIN) as BinarySizesData;
  const version = useDocsVersion().version;
  const current = data.currentVersions.includes(version);
  const manifest = current ? data.manifest : data.archives[version] ?? null;
  if (!manifest) return <Admonition type="note" title="No recorded release build data">This documentation build has no binary-size manifest for {version}. Figures are left unavailable rather than borrowed from another release.</Admonition>;
  const rows = ['amd64', 'arm64'].map((arch) => ({
    arch,
    go: manifest.binaries.find((binary) => binary.os === 'linux' && binary.arch === arch && binary.compiler === 'go')!,
    tinygo: manifest.binaries.find((binary) => binary.os === 'linux' && binary.arch === arch && binary.compiler === 'tinygo')!,
  }));
  return <>
    <table><thead><tr><th>Linux target</th><th>Go</th><th>TinyGo</th><th>TinyGo / Go</th></tr></thead><tbody>
      {rows.map(({arch, go, tinygo}) => <tr key={arch}><td>{arch}</td><td>{mib(go.bytes)}</td><td>{mib(tinygo.bytes)}</td><td>{(tinygo.bytes / go.bytes * 100).toFixed(1)}%</td></tr>)}
    </tbody></table>
    <p><em>Measured from the published assets for <a href={`https://github.com/yohimik/dispat/releases/tag/services/dispat/v${manifest.version}`}>release {manifest.version}</a>, at <a href={`https://github.com/yohimik/dispat/commit/${manifest.sourceCommit}`}>source commit {manifest.sourceCommit.slice(0, 12)}</a>. Toolchains: {manifest.toolchains.go}; {manifest.toolchains.tinygo}.</em></p>
  </>;
}
