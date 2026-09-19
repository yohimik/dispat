#!/usr/bin/env python3
"""Shellcheck the shell that lives inside the dispat configuration files.

Every `scripts:` entry of a dispat config is a shell command, and until this
existed none of it was linted: the shell sweep reads files, and these are
values inside YAML. The reader here is deliberately narrow — it understands
the four shapes a `scripts` entry is written in and refuses anything else
rather than skipping it, because a gate that silently reads nothing is worse
than no gate.

Each command becomes one file and is checked twice: `sh -n`, which is the
interpreter's own answer about whether it parses, and shellcheck, which is
the answer about whether it means what it says.

  usage: lint-config-scripts.py <config>...
         lint-config-scripts.py --self-test
"""
import subprocess
import sys
import tempfile
from pathlib import Path

# The buildx cache arguments are a list of separate flags produced by a
# command substitution, and every gate in this repository splits them on
# purpose, exactly as the workflows do with an inline disable comment. Those
# two codes are the whole of that pattern, so they are named here once rather
# than in forty places. Everything else at warning severity is a finding.
EXCLUDED = 'SC2046,SC2086'
SEVERITY = 'warning'


class Refused(Exception):
    """A configuration shape this reader will not guess at."""


def entries(path):
    """Yield (name, [commands]) for every scripts entry of one config file."""
    lines = Path(path).read_text().splitlines()
    i = 0
    while i < len(lines) and lines[i].rstrip() != 'scripts:':
        i += 1
    i += 1
    while i < len(lines):
        line = lines[i]
        if not line.strip() or line.lstrip().startswith('#'):
            i += 1
            continue
        if not line.startswith('  '):
            break
        if line.startswith('   '):
            raise Refused(f'{path}:{i + 1}: {line!r} is indented past a scripts entry')
        head, sep, rest = line[2:].partition(': ')
        if sep:
            name, marker = head, rest.strip()
        elif line.rstrip().endswith(':'):
            name, marker = line[2:].rstrip()[:-1], ''
        else:
            raise Refused(f'{path}:{i + 1}: cannot read the scripts entry {line.strip()!r}')
        i += 1
        commands, i = value(path, lines, i, marker, name)
        yield name, commands


def value(path, lines, i, marker, name):
    """Read one entry's value: a block, a folded block, a string or a list."""
    if marker.startswith('|'):
        text, i = block(lines, i, 4)
        return [text], i
    if marker.startswith('>'):
        text, i = block(lines, i, 4)
        return [fold(text)], i
    if marker:
        return [marker], i
    commands = []
    while i < len(lines):
        line = lines[i]
        if not line.strip() or line.lstrip().startswith('#'):
            i += 1
            continue
        if not line.startswith('    - '):
            break
        item = line[6:].strip()
        i += 1
        if item.startswith('|'):
            text, i = block(lines, i, 6)
            commands.append(text)
        elif item.startswith('>'):
            text, i = block(lines, i, 6)
            commands.append(fold(text))
        elif item:
            commands.append(item)
        else:
            raise Refused(f'{path}: the scripts entry {name!r} has an empty list item')
    if not commands:
        raise Refused(f'{path}: the scripts entry {name!r} binds no command')
    return commands, i


def block(lines, i, indent):
    """Read the body of a block scalar indented by `indent` spaces."""
    out = []
    while i < len(lines):
        line = lines[i]
        if not line.strip():
            out.append('')
            i += 1
            continue
        if not line.startswith(' ' * indent):
            break
        out.append(line[indent:])
        i += 1
    while out and not out[-1]:
        out.pop()
    if not out:
        raise Refused('a block scalar with no body')
    return '\n'.join(out), i


def fold(text):
    """Join a folded scalar the way YAML does: one line, single spaces."""
    return ' '.join(part.strip() for part in text.splitlines() if part.strip())


def check(paths):
    """Write every command to a file and hold all of them to both checkers."""
    written = []
    with tempfile.TemporaryDirectory(prefix='dispat-config-scripts-') as folder:
        for path in paths:
            for name, commands in entries(path):
                for n, command in enumerate(commands):
                    slug = f'{path}-{name}-{n}'.replace('/', '-').replace(':', '-')
                    file = Path(folder, slug.lstrip('.-') + '.sh')
                    file.write_text('#!/bin/sh\n' + command + '\n')
                    written.append((f'{path}: {name}[{n}]', file))
        if not written:
            print('no scripts found in: ' + ' '.join(paths), file=sys.stderr)
            return 1
        failed = 0
        for label, file in written:
            parsed = subprocess.run(['sh', '-n', str(file)], capture_output=True, text=True)
            if parsed.returncode != 0:
                print(f'{label}: does not parse\n{parsed.stderr}', file=sys.stderr)
                failed += 1
        linted = subprocess.run(
            ['shellcheck', '-s', 'sh', f'--severity={SEVERITY}', f'--exclude={EXCLUDED}']
            + [str(file) for _, file in written],
            capture_output=True, text=True)
        if linted.returncode != 0:
            names = dict((str(file), label) for label, file in written)
            out = linted.stdout
            for file, label in names.items():
                out = out.replace(file, label)
            print(out, file=sys.stderr)
            print(linted.stderr, file=sys.stderr, end='')
            failed += 1
        print(f'config scripts: {len(written)} commands from {len(paths)} files')
        return 1 if failed else 0


FIXTURE = '''\
scripts:
  # A comment between entries, and one at column zero below.
  inline: echo one
# Not part of any entry.
  block: |
    set -eu
    echo two
  folded: >-
    echo
    three
  sequence:
    - echo four
    - >-
      echo
      five
    - |
      echo six
other: value
'''

REFUSED = 'scripts:\n  broken\n'


def self_test():
    """Hold the reader to the four shapes and to refusing a fifth."""
    with tempfile.TemporaryDirectory(prefix='dispat-config-fixture-') as folder:
        good = Path(folder, 'dispat.yaml')
        good.write_text(FIXTURE)
        found = dict(entries(str(good)))
        want = {'inline': ['echo one'], 'block': ['set -eu\necho two'],
                'folded': ['echo three'],
                'sequence': ['echo four', 'echo five', 'echo six']}
        if found != want:
            print(f'self-test: read {found!r}, want {want!r}', file=sys.stderr)
            return 1
        bad = Path(folder, 'bad.yaml')
        bad.write_text(REFUSED)
        try:
            list(entries(str(bad)))
        except Refused:
            print('config scripts: the reader holds and refuses what it cannot read')
            return 0
        print('self-test: an unreadable entry was not refused', file=sys.stderr)
        return 1


if __name__ == '__main__':
    if sys.argv[1:2] == ['--self-test']:
        sys.exit(self_test())
    if len(sys.argv) < 2:
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    try:
        sys.exit(check(sys.argv[1:]))
    except Refused as refused:
        print(f'config scripts: {refused}', file=sys.stderr)
        sys.exit(1)
