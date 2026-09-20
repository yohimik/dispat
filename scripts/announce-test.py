#!/usr/bin/env python3
"""Check the release announcement without contacting any platform.

The announce script is a few lines of `dispat if` in services/dispat/dispat.yaml,
so the part of this check that runs it needs a dispat binary: DISPAT_TEST_BINARY,
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


# --- notes.py: the generated notes a stable release announces ---------------
def notes(**overrides):
    result = subprocess.run([sys.executable, str(announcement_root / 'notes.py')], cwd=tempfile.gettempdir(),
                            env={**base, **overrides}, capture_output=True, text=True, check=True)
    return json.loads(result.stdout)


data = notes(DISPAT_NEW_VERSION='1.8.0', DISPAT_FEATURES='Aqua support', DISPAT_FIXES='Lock ownership\n',
             DISPAT_BREAKING_CHANGES='', DISPAT_DEPENDENCIES='models: 1.7.0 -> 1.8.0')
assert [(s['label'], s['items'], s['more']) for s in data['sections']] == [
    ('FEATURES', ['Aqua support'], 0), ('FIXES', ['Lock ownership'], 0), ('PICKS UP', ['models: 1.7.0 -> 1.8.0'], 0)]
assert data['version'] == '1.8.0' and all('1.8.0' in route['command'] for route in data['install'])
assert 'coveronly' not in data and notes(DISPAT_NEW_VERSION='1.8.0', ANNOUNCE_COVER_ONLY='1')['coveronly']
# A release subject is data: quotes, backslashes and tabs must survive JSON.
subject = 'A "quoted" saga \\ path\tfirst'
assert notes(DISPAT_NEW_VERSION='1.8.0', DISPAT_FIXES=subject)['sections'][0]['items'] == [subject]
many = notes(DISPAT_NEW_VERSION='1.8.0', DISPAT_FIXES='\n'.join(f'fix {n}' for n in range(25)))['sections'][0]
assert len(many['items']) == 20 and many['more'] == 5
checked.append('generated stable notes')

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
assert stable['data'] == ['-'] and '{{ range .sections }}' in stable['caption'] and '.sections' in stable['card']
assert rc['data'] == ['env:DISPAT_'], 'an rc takes only the version, from the environment'
assert '  caption:\n    $ref: notes.yaml\n' in (announcement_root / 'rc/crier.yaml').read_text(), \
    'the rc words live in rc/notes.yaml, so a new candidate never edits the configuration'
rc_markup = re.sub(r'/\*.*?\*/', '', rc['card'].split('<!doctype html>', 1)[1], flags=re.S)
# Every field the rc reads, in its caption and on its card: the version and
# nothing dispat generated.
for where, text in (('caption', rc['caption']), ('card', rc_markup)):
    fields = set(re.findall(r'(?<![\w$])\.[A-Za-z_]\w*', ' '.join(re.findall(r'\{\{(.*?)\}\}', text, flags=re.S))))
    assert '.new_version' in fields and fields <= {'.new_version', '.Platform'}, f'the rc {where} reads {sorted(fields)}'


def resolve(caption, platform, version):
    """Evaluate the two constructs the rc caption uses: the version and the Discord branch."""
    def branch(match):
        then, _, otherwise = match.group(1).partition('{{ else }}')
        return then if platform == 'discord' else otherwise
    text = re.sub(r'\{\{ if eq \.Platform "discord" \}\}(.*?)\{\{ end \}\}', branch, caption, flags=re.S)
    text = text.replace('{{ .new_version }}', version)
    assert '{{' not in text, 'the rc caption uses a template construct this check cannot read'
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

# --- the announce script in services/dispat/dispat.yaml ----------------------
config = (package / 'dispat.yaml').read_text()
block = config.split('\n  announce: |\n', 1)[1]
script = '\n'.join(line[4:] for line in re.match(r'((?:    .*\n|\n)+)', block).group(1).splitlines())
assert 'announce.sh' not in config and not list(announcement_root.glob('*.sh'))
for needle in ("dispat if 'DISPAT_STAGE!=announce'", "--elif '!ANNOUNCE'",
               "--elif 'DISPAT_CHANNEL=rc' --then 'crier publish --config announce/rc/crier.yaml'",
               "--elif 'DISPAT_CHANNEL=stable' --then 'python3 announce/notes.py | crier publish"
               " --config announce/stable/crier.yaml'"):
    assert needle in script, needle
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

        code, calls, _ = run()
        assert code == 0 and len(calls) == 1, 'a stable release makes exactly one crier call'
        assert calls[0]['args'] == ['publish', '--config', 'announce/stable/crier.yaml']
        assert json.loads(calls[0]['stdin'])['sections'][0]['items'] == ['Lock ownership']
        for version in ('1.11.0-rc.0', '1.11.0-rc.1'):
            code, calls, _ = run(DISPAT_CHANNEL='rc', DISPAT_NEW_VERSION=version)
            assert code == 0 and len(calls) == 1, 'an rc makes exactly one crier call'
            assert calls[0]['args'] == ['publish', '--config', 'announce/rc/crier.yaml']
            assert calls[0]['stdin'] == '', 'an rc is announced without generated notes'
        for overrides in ({'DISPAT_STAGE': 'run:announce'}, {'ANNOUNCE': ''}, {'DISPAT_CHANNEL': 'beta'}):
            code, calls, out = run(**overrides)
            assert code == 0 and calls == [] and 'announce:' in out, f'{overrides} must post nothing and say why'
        code, calls, _ = run(ANNOUNCE_TEST_EXIT='1')
        assert code == 1 and len(calls) == 1, 'an incomplete announcement must fail without a retry'
    checked.append('announce script run with dispat if')
print('announcement flow: ' + ', '.join(checked) + ' passed')
