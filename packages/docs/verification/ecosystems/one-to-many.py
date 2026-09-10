#!/usr/bin/env python3
"""Verify the documented one-to-many graph in a disposable repository."""
import json
import pathlib
import subprocess
import tempfile

with tempfile.TemporaryDirectory(prefix='dispat-one-to-many-') as temp:
    root = pathlib.Path(temp)

    def run(*args):
        return subprocess.check_output(args, cwd=root, text=True, stderr=subprocess.STDOUT)

    run('git', 'init', '-q')
    run('git', 'config', 'user.name', 'Verification')
    run('git', 'config', 'user.email', 'verification@example.invalid')
    (root / 'core').mkdir()
    (root / 'core/source.txt').write_text('unchanged core source\n')
    config = {'packages': {'core': {'path': 'core'}}, 'initials': {'core': '1.2.0'}}
    (root / 'dispat.json').write_text(json.dumps(config))
    run('git', 'add', '.')
    run('git', 'commit', '-qm', 'chore(core): baseline')
    # Local fixture baseline only; never create production release tags by hand.
    run('git', 'tag', 'core@1.2.0')
    baseline = run('git', 'rev-parse', 'core@1.2.0')
    original = config['packages']['core'].copy()
    before = [json.loads(line) for line in run('dispat', 'status', '--log-format', 'json').splitlines()]
    assert next(e for e in before if e.get('message') == 'release plan ready')['releasing'] == 0
    for name, provider in [('cli', 'core'), ('image', 'cli'), ('docs', 'core'), ('site', None)]:
        (root / name).mkdir()
        (root / name / 'source.txt').write_text(name + '\n')
        package = {'path': name}
        if provider:
            package['dependencies'] = [{'provider': provider, 'keep': True}]
        if name == 'cli':
            package['isBuildWaitingPublish'] = True
        config['packages'][name] = package
        config['initials'][name] = '0.1.0'
    assert config['packages']['core'] == original
    (root / 'dispat.json').write_text(json.dumps(config, indent=2) + '\n')
    run('git', 'add', '.')
    run('git', 'commit', '-qm', 'chore: add deliverables')
    run('git', 'commit', '--allow-empty', '-qm', 'fix(core)^^: exercise consumers')
    output = run('dispat', 'status', '--log-format', 'json')
    events = [json.loads(line) for line in output.splitlines()]
    changed = {e['package']: e['version'] for e in events if e.get('message') == '● changed'}
    assert changed == {'core': '1.2.0 -> 1.2.1', 'cli': '0.1.0 -> 0.1.1',
                       'image': '0.1.0 -> 0.1.1', 'docs': '0.1.0 -> 0.1.1'}, changed
    assert run('git', 'rev-parse', 'core@1.2.0') == baseline
    assert (root / 'core/source.txt').read_text() == 'unchanged core source\n'
    print(output, end='')
    print('Verified: baseline and core entry preserved; four planned releases; site unchanged.')
