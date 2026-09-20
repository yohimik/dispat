#!/usr/bin/env python3
"""Check the release announcement without contacting any platform.

The announce script is one `dispat if` line in services/dispat/dispat.yaml, so
the part of this check that runs it needs a dispat binary: DISPAT_TEST_BINARY,
or `dispat` on PATH. Without one (the alpine shell gate has none) that part says
it was skipped, and everything that reads files still runs.
"""
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile

root = Path(__file__).resolve().parents[1]
package = root / 'services/dispat'
announcement_root = package / 'announce'
checked = []
base = {k: v for k, v in os.environ.items() if not k.startswith(('ANNOUNCE', 'CRIER_', 'DISPAT_', 'NGROK_'))}


# --- two crier configurations: same cross-posting, own card, music, caption --
shared = (announcement_root / 'publish.yaml').read_text()
assert re.findall(r'^(\w+):\n  enabled: true$', shared, flags=re.M) == ['instagram', 'linkedin', 'discord']
assert 'caption:' not in re.sub(r'#.*', '', shared), 'captions belong to the channels, destinations to publish.yaml'
channels = {}
for channel in ('rc', 'stable'):
    folder = announcement_root / channel
    render, _, publish = (folder / 'crier.yaml').read_text().partition('\npublish:\n')
    assert publish.startswith('  $ref: ../publish.yaml\n'), f'{channel} must take its cross-posting from publish.yaml'
    beside = re.findall(r'^  ([\w$-]+):', publish, flags=re.M)
    assert beside == ['$ref', 'caption'], f'{channel} may write only its caption beside the shared destinations'
    # A caption is written in place, or pulled from a file of the channel's own
    # so that announcing never means editing the configuration.
    if '  caption:\n    $ref: ' in publish:
        words = (folder / publish.split('  caption:\n    $ref: ', 1)[1].split()[0]).read_text()
        caption = '\n'.join(line[2:] for line in words.split('--- |-\n', 1)[1].splitlines())
    else:
        caption = '\n'.join(line[4:] for line in publish.split('  caption: |-\n', 1)[1].splitlines())
    template = [line.split(':', 1)[1].strip() for line in render.splitlines() if line.startswith('  template:')]
    music = [line.strip()[2:] for line in render.splitlines() if line.startswith('      - ')]
    source = [line.split(':', 1)[1].strip().strip('"') for line in render.splitlines() if line.startswith('  data:')]
    assert template == ['template.html'] and len(music) >= 1 and all(clip.endswith('.mp3') for clip in music)
    channels[channel] = {'card': (folder / template[0]).read_text(), 'music': set(music),
                         'caption': caption, 'data': source}
rc, stable = channels['rc'], channels['stable']
assert rc['card'] != stable['card'], 'the channels must not share a card'
assert not rc['music'] & stable['music'], 'the channels must not share music'
checked.append('shared destinations with separate cards and music')

# --- stable: dispat's generated notes; rc: only what a person wrote ----------
# Both take their data from the announce stage's environment, so nothing builds
# a document first and nothing beside crier runs.
assert rc['data'] == stable['data'] == ['env:DISPAT_']
assert not list(announcement_root.glob('*.sh')) and not list(announcement_root.glob('*.py'))
assert '  caption:\n    $ref: notes.yaml\n' in (announcement_root / 'rc/crier.yaml').read_text(), \
    'the rc words live in rc/notes.yaml, so a new candidate never edits the configuration'


def fields(text):
    """Every data field a template reads."""
    return set(re.findall(r'(?<![\w$])\.[A-Za-z_]\w*', ' '.join(re.findall(r'\{\{(.*?)\}\}', text, flags=re.S))))


def markup(card):
    return re.sub(r'/\*.*?\*/', '', card.split('<!doctype html>', 1)[1], flags=re.S)


GROUPS = {'.breaking_changes', '.features', '.fixes', '.dependencies'}
rc_markup, stable_markup = markup(rc['card']), markup(stable['card'])
# An rc reads the version and nothing dispat generated; a stable release reads
# the version and exactly the four release-notes groups dispat documents.
for where, text in (('caption', rc['caption']), ('card', rc_markup)):
    assert fields(text) - {'.Platform'} == {'.new_version'}, f'the rc {where} reads {sorted(fields(text))}'
for where, text in (('caption', stable['caption']), ('card', stable_markup)):
    assert fields(text) - {'.Platform'} == {'.new_version'} | GROUPS, f'the stable {where} reads {sorted(fields(text))}'


def resolve(caption, platform, version, groups=None):
    """Evaluate the constructs the captions use, the way crier would.

    The platform branch, the version, and for a stable caption the groups: one
    printed while it is set, and replaced by a pointer once it is too long.
    """
    groups = groups or {}

    def branch(match):
        then, _, otherwise = match.group(1).partition('{{ else }}')
        return then if platform == 'discord' else otherwise

    def group(match):
        name, limit = match.group(1), int(match.group(2))
        value = groups.get(name, '')
        return value if len(value) < limit else match.group(3)

    def present(match):
        return match.group(2) if groups.get(match.group(1), '') else ''

    text = re.sub(r'\{\{ if lt \(len \.(\w+)\) (\d+) \}\}\{\{ \.\1 \}\}\{\{ else \}\}(.*?)\{\{ end \}\}', group, caption)
    text = re.sub(r'\{\{ if \.(\w+) \}\}((?:(?!\{\{ if ).)*?)\{\{ end \}\}', present, text, flags=re.S)
    text = re.sub(r'\{\{ if eq \.Platform "discord" \}\}(.*?)\{\{ end \}\}', branch, text, flags=re.S)
    text = text.replace('{{ .new_version }}', version)
    assert '{{' not in text, f'a caption uses a template construct this check cannot read: {text[text.index("{{"):][:60]}'
    return text


LINK = r'^(.+?): (https://\S+)$'
# The words are duplicated by hand in the caption and on the card: every
# paragraph and every link a description prints must be on the picture, and the
# picture must say nothing else.
full = resolve(rc['caption'], 'instagram', '@VERSION@')
rc_markup = rc_markup.replace('{{ .new_version }}', '@VERSION@')
links = re.findall(LINK, full, flags=re.M)
assert len(links) >= 2 and any(url.endswith('/releases/tag/services/dispat/v@VERSION@') for _, url in links), \
    'an rc links to its own release'
for label, url in links:
    assert f'<span>{label}</span> {url}</div>' in rc_markup, f'the card is missing the link: {label}'
assert len(re.findall(r'class="cmd link"', rc_markup)) == len(links), 'the card has a link its caption does not'
paragraphs = [line for line in full.splitlines()[1:]
              if line and not line.startswith('#') and not re.match(LINK, line)]
written = [text.strip() for text in re.findall(r'<(?:p|div class="lede")>(.*?)</(?:p|div)>', rc_markup, flags=re.S)]
assert len(paragraphs) >= 2 and written == paragraphs, 'the rc card and its caption must say the same words'
# crier posts a caption whole and the platforms refuse a long one.
for platform, limit in (('discord', 2000), ('instagram', 2200), ('linkedin', 3000)):
    length = len(resolve(rc['caption'], platform, '1.11.0-rc.10+build.20260920'))
    assert length <= limit, f'the rc caption is {length} characters on {platform}, over its {limit}'
on_discord = resolve(rc['caption'], 'discord', '1.11.0-rc.1')
assert on_discord.startswith('@everyone dispat v1.11.0-rc.1 ') and not full.startswith('@everyone')
assert re.findall(LINK, on_discord, flags=re.M) == re.findall(LINK, resolve(rc['caption'], 'x', '1.11.0-rc.1'), flags=re.M), \
    'Discord keeps every link'
checked.append('rc words duplicated on its card and inside platform limits')

# A stable caption prints a group only while it is short, so that the worst
# case still fits: every group one character under its limit, on every platform.
limits = [int(n) for n in re.findall(r'\{\{ if lt \(len \.\w+\) (\d+) \}\}', stable['caption'])]
assert len(limits) == len(GROUPS)
worst = {name.lstrip('.'): 'x' * (limit - 1) for name, limit in zip(sorted(GROUPS), limits)}
huge = {name.lstrip('.'): 'entry\n' * 400 for name in GROUPS}
for platform, limit in (('discord', 2000), ('instagram', 2200), ('linkedin', 3000)):
    for groups in (worst, huge, {}):
        length = len(resolve(stable['caption'], platform, '1.11.0+build.20260920', groups))
        assert length <= limit, f'the stable caption can reach {length} characters on {platform}, over its {limit}'
sample = resolve(stable['caption'], 'linkedin', '1.11.0', {'features': 'Aqua support\nLock ownership', 'fixes': 'x' * 400})
assert 'FEATURES:\nAqua support\nLock ownership' in sample and 'FIXES:\nsee the pictures' in sample
assert 'BREAKING' not in sample and '/releases/tag/services/dispat/v1.11.0\n' in sample
assert resolve(stable['caption'], 'discord', '1.11.0', worst).startswith('@everyone dispat v1.11.0 is out.')
checked.append('stable groups inside platform limits')

# --- the announce script in services/dispat/dispat.yaml ----------------------
config = (package / 'dispat.yaml').read_text()
script = ' '.join(line.strip() for line in
                  re.match(r'((?:    .*\n)+)', config.split('\n  announce: >-\n', 1)[1]).group(1).splitlines())
assert script == ("dispat if ANNOUNCE --then 'crier publish --config \"announce/$DISPAT_CHANNEL/crier.yaml\"' "
                  "--else 'echo \"announce: announcements are off for this run\"'"), script
assert script.count('crier ') == 1, 'one crier command, and the channel names its configuration'
# The step outputs belong to postPublish, so they never wait on a social network.
released = re.match(r'((?:    .*\n)+)', config.split('\n  released: |\n', 1)[1]).group(1)
assert 'cli-released=true' in released and 'cli-version=$DISPAT_NEW_VERSION' in released and 'GITHUB_OUTPUT' not in script
assert re.search(r'^flow:\n(?:  .*\n)*?  postPublish: released\n(?:  .*\n)*?  announce: announce\n', config, flags=re.M)
checked.append('announce script shape')

dispat = os.environ.get('DISPAT_TEST_BINARY') or shutil.which('dispat')
if not dispat:
    print('announcement flow: no dispat binary here, so the announce script was read but not run')
else:
    with tempfile.TemporaryDirectory() as work:
        work = Path(work)
        bin_dir = work / 'bin'
        bin_dir.mkdir()
        (bin_dir / 'crier').write_text(f'''#!{sys.executable}
import json, os, sys
with open(os.environ['ANNOUNCE_TEST_LOG'], 'a') as out:
    out.write(json.dumps({{'args': sys.argv[1:], 'stdin': sys.stdin.read()}})+'\\n')
sys.exit(int(os.environ.get('ANNOUNCE_TEST_EXIT', '0')))
''')
        (bin_dir / 'crier').chmod(0o755)
        (bin_dir / 'dispat').symlink_to(Path(dispat).resolve())
        log = work / 'calls.jsonl'
        env = {**base, 'PATH': f"{bin_dir}{os.pathsep}{base.get('PATH', '')}", 'ANNOUNCE_TEST_LOG': str(log),
               'ANNOUNCE': 'true', 'DISPAT_STAGE': 'announce', 'DISPAT_CHANNEL': 'stable',
               'DISPAT_NEW_VERSION': '1.8.0', 'DISPAT_FIXES': 'Lock ownership'}

        def run(**overrides):
            log.write_text('')
            result = subprocess.run(['sh', '-c', script], cwd=package, env={**env, **overrides},
                                    stdin=subprocess.DEVNULL, capture_output=True, text=True)
            return result.returncode, [json.loads(line) for line in log.read_text().splitlines()], result.stdout

        for channel in ('stable', 'rc', 'beta'):
            code, calls, _ = run(DISPAT_CHANNEL=channel)
            assert code == 0 and len(calls) == 1, 'exactly one crier call'
            assert calls[0]['args'] == ['publish', '--config', f'announce/{channel}/crier.yaml']
            assert calls[0]['stdin'] == '', 'crier reads the environment, so nothing is piped into it'
        code, calls, out = run(ANNOUNCE='')
        assert code == 0 and calls == [] and 'announcements are off' in out, 'without ANNOUNCE nothing is posted'
        code, calls, _ = run(ANNOUNCE_TEST_EXIT='1')
        assert code == 1 and len(calls) == 1, 'an incomplete announcement must fail without a retry'
    checked.append('announce script run with dispat if')
print('announcement flow: ' + ', '.join(checked) + ' passed')
