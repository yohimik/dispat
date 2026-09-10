#!/usr/bin/env python3
"""Check pinned public manifests without running upstream build or publish scripts."""
import argparse
import datetime
import hashlib
import json
import pathlib
import re
import shutil
import subprocess
import tempfile
import urllib.parse
import urllib.request


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--dispat', default='dispat')
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args()
    binary = shutil.which(args.dispat)
    if binary is None:
        parser.error('dispat executable not found')
    cases = json.loads(pathlib.Path(__file__).with_name('cases.json').read_text())
    expected_ecosystems = {'npm', 'gomod', 'cargo', 'python', 'composer', 'maven', 'nuget',
                           'pub', 'plist', 'cocoapods', 'xcode', 'android', 'gradle',
                           'rubygems', 'docker', 'aqua', 'unity', 'godot', 'unreal',
                           'defold', 'o3de'}
    assert len(cases) == 21 and {c['ecosystem'] for c in cases} == expected_ecosystems
    version = subprocess.check_output([binary, '--version'], text=True)
    report = {'verifiedAt': datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'binary': version.strip(), 'scope': 'manifest scan/write/readback only', 'cases': []}
    with tempfile.TemporaryDirectory(prefix='dispat-public-manifests-') as temp:
        for case in cases:
            eco = case['ecosystem']
            cwd = pathlib.Path(temp) / eco
            path = pathlib.PurePosixPath(case['path'])
            assert not path.is_absolute() and '..' not in path.parts
            assert re.fullmatch(r'[0-9a-f]{40}', case['revision'])
            target = cwd / path
            target.parent.mkdir(parents=True)
            url = (f"https://raw.githubusercontent.com/{case['repo']}/{case['revision']}/"
                   + urllib.parse.quote(case['path']))
            original = urllib.request.urlopen(url, timeout=60).read()
            assert digest(original) == case['sha256'], f'{eco}: upstream content hash mismatch'
            target.write_bytes(original)
            log = []

            def run(command):
                result = subprocess.run([binary, '--log-format', 'json', *command], cwd=cwd,
                                        text=True, capture_output=True, timeout=60)
                events = [json.loads(line) for line in (result.stdout + result.stderr).splitlines()
                          if line.startswith('{')]
                log.append({'command': command, 'exit': result.returncode, 'events': events})
                assert result.returncode == 0, f'{eco}: {result.stderr or result.stdout}'
                assert not any(e.get('level') == 'error' for e in events), f'{eco}: error diagnostic'
                return events

            def scan():
                events = run(['scanner', '.', '--strict'])
                manifests = [e for e in events if e.get('message') == 'manifest']
                assert len(manifests) == 1 and manifests[0]['path'] == case['path']
                return {k: v for k, v in manifests[0].items()
                        if k not in ('level', 'time', 'message', 'root', 'path')}

            assert scan() == case['before'], f'{eco}: scanner result changed'
            command = ['writer', case['path'], *case['writerArgs'], '--strict']
            events = run(command)
            event = next(e for e in events if e.get('path') == case['path'])
            assert {k: event[k] for k in case['expectWriter']} == case['expectWriter'], eco
            assert digest(target.read_bytes()) == case['afterSha256'], f'{eco}: unexpected file edit'
            assert scan() == case['after'], f'{eco}: edit did not read back as expected'
            run(command)
            assert digest(target.read_bytes()) == case['afterSha256'], f'{eco}: edit is not idempotent'
            report['cases'].append({'ecosystem': eco, 'source': url, 'sha256': case['sha256'],
                                    'afterSha256': case['afterSha256'], 'passed': True, 'runs': log})
            print(f'{eco}: scan, edit/skip, readback and idempotence passed', flush=True)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    main()
