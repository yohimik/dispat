#!/usr/bin/env python3
"""Check the release's single Crier invocation without contacting any platform."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
announcement_root = root / 'services/dispat/announce'
with tempfile.TemporaryDirectory() as work:
    work = Path(work)
    fake = work / 'crier'
    fake.write_text('''#!/usr/bin/env python3
import json, os, sys
with open(os.environ['ANNOUNCE_TEST_LOG'], 'a') as out:
    out.write(json.dumps({'args': sys.argv[1:], 'stage': os.environ.get('CRIER_STAGE_MODE'), 'data': json.load(sys.stdin)})+'\\n')
sys.exit(int(os.environ.get('ANNOUNCE_TEST_EXIT', '0')))
''')
    fake.chmod(0o755)
    log = work / 'calls.jsonl'
    env = {k:v for k,v in os.environ.items() if not k.startswith(('ANNOUNCE_', 'CRIER_', 'DISPAT_', 'NGROK_'))}
    env.update(DISPAT_STAGE='announce', DISPAT_NEW_VERSION='1.8.0',
               DISPAT_FEATURES='Aqua support', DISPAT_FIXES='Lock ownership',
               CRIER_PUBLISH_INSTAGRAM_TOKEN='test', CRIER_PUBLISH_INSTAGRAM_USER_ID='test',
               CRIER_PUBLISH_LINKEDIN_TOKEN='test', CRIER_PUBLISH_LINKEDIN_AUTHOR_URN='test',
               CRIER_PUBLISH_DISCORD_WEBHOOK_URL='https://discord.com/api/webhooks/123/test',
               CRIER_STAGE_MODE='url', ANNOUNCE_CRIER_BIN=str(fake), ANNOUNCE_TEST_LOG=str(log))

    def copy(channel, version):
        """The committed notes and links of a channel, as notes.sh must report them."""
        folder = announcement_root / channel
        notes = (folder / 'announcement.md').read_text().replace('${DISPAT_NEW_VERSION}', version).splitlines()
        links = []
        for line in (folder / 'links.md').read_text().replace('${DISPAT_NEW_VERSION}', version).splitlines():
            if line.strip():
                label, url = line.split(': ', 1)
                links.append({'label': label, 'url': url})
        return notes, links

    def run(**overrides):
        log.write_text('')
        result = subprocess.run(['sh', 'services/dispat/announce/announce.sh'], cwd=root,
                                env={**env, **overrides}, capture_output=True, text=True)
        return result.returncode, [json.loads(line) for line in log.read_text().splitlines()]

    code, calls = run()
    assert code == 0 and len(calls) == 1
    call = calls[0]
    assert call['args'][0] == 'publish'
    for platform in ('instagram', 'linkedin', 'discord'):
        assert f'--publish-{platform}-enabled=true' in call['args']
    assert '--render-video-enabled=false' in call['args']
    assert call['args'][call['args'].index('--render-pages-max')+1] == '10'
    assert call['data']['sections'][0]['items'] == ['Aqua support']
    assert call['data']['sections'][1]['items'] == ['Lock ownership']
    assert call['data']['channel'] == 'stable'
    assert (call['data']['announcement'], call['data']['links']) == copy('stable', '1.8.0')
    assert Path(call['args'][call['args'].index('--config')+1]) == announcement_root / 'stable/crier.yaml'
    for version in ('1.11.0-rc.0', '1.11.0-rc.1', '1.11.0-rc.1+build.2'):
        code, calls = run(DISPAT_NEW_VERSION=version)
        assert code == 0 and len(calls) == 1
        args = calls[0]['args']
        assert Path(args[args.index('--config')+1]) == announcement_root / 'rc/crier.yaml'
        data = calls[0]['data']
        assert data['channel'] == 'rc'
        assert (data['announcement'], data['links']) == copy('rc', version)
        # An rc links to itself: the release of this exact version, not the list.
        assert any(link['url'].endswith(f'/releases/tag/services/dispat/v{version}') for link in data['links'])
        assert '--topology minimal' in '\n'.join(data['announcement'])
        assert '--topology star' in '\n'.join(data['announcement'])
        assert all(version in route['command'] for route in data['install'])
    code, calls = run(DISPAT_NEW_VERSION='1.11.0+build.rc.1')
    assert code == 0 and calls[0]['data']['channel'] == 'stable'
    code, calls = run(DISPAT_NEW_VERSION='1.11.0-beta.0')
    assert code != 0 and calls == [], 'an unsupported channel must not publish stable copy'
    code, calls = run(ANNOUNCE_COVER_ONLY='1')
    assert code == 0 and len(calls) == 1 and calls[0]['data']['coveronly']
    assert calls[0]['data']['sections'][1]['items'] == ['Lock ownership']
    code, calls = run(ANNOUNCE_TEST_EXIT='1')
    assert code == 1 and len(calls) == 1, 'partial publishing must fail without a retry'
    for platform in ('linkedin', 'discord'):
        code, calls = run(ANNOUNCE_ONLY=platform, CRIER_PUBLISH_INSTAGRAM_TOKEN='', CRIER_STAGE_MODE='server')
        assert code == 0 and len(calls) == 1 and calls[0]['stage'] == 'none'
        for destination in ('instagram', 'linkedin', 'discord'):
            enabled = str(destination == platform).lower()
            assert f'--publish-{destination}-enabled={enabled}' in calls[0]['args']
    for overrides in ({'CRIER_PUBLISH_INSTAGRAM_TOKEN':''}, {'CRIER_PUBLISH_LINKEDIN_TOKEN':''},
                      {'CRIER_STAGE_MODE':'server'}, {'ANNOUNCE_ONLY':'unknown'},
                      {'ANNOUNCE_ONLY':'discord', 'CRIER_PUBLISH_DISCORD_WEBHOOK_URL':''}):
        code, calls = run(**overrides)
        assert code == 1 and calls == []
    for overrides in ({'DISPAT_STAGE':'run:announce'}, {'DISPAT_NEW_VERSION':''}):
        code, calls = run(**overrides)
        assert code == 0 and calls == []
    # A dispatch that skips announcements withholds every platform credential.
    code, calls = run(CRIER_PUBLISH_INSTAGRAM_TOKEN='', CRIER_PUBLISH_INSTAGRAM_USER_ID='',
                      CRIER_PUBLISH_LINKEDIN_TOKEN='', CRIER_PUBLISH_LINKEDIN_AUTHOR_URN='',
                      CRIER_PUBLISH_DISCORD_WEBHOOK_URL='')
    assert code == 0 and calls == [], 'no platform credentials must skip every publisher'
    # Two crier configurations, one per channel: the same cross-posting from the
    # one file both pull in, and a card and music of each channel's own.
    channels = {}
    for channel in ('rc', 'stable'):
        folder = announcement_root / channel
        render, _, publish = (folder / 'crier.yaml').read_text().partition('\npublish:\n')
        assert publish == '  $ref: ../publish.yaml\n', f'{channel} must take its whole cross-posting from publish.yaml'
        template = [line.split(':', 1)[1].strip() for line in render.splitlines() if line.startswith('  template:')]
        music = [line.strip()[2:] for line in render.splitlines() if line.startswith('      - ')]
        assert template == ['template.html'] and len(music) >= 1 and all(clip.endswith('.mp3') for clip in music)
        channels[channel] = ((folder / template[0]).read_bytes(), set(music))
    assert channels['rc'][0] != channels['stable'][0], 'the channels must not share a card'
    assert not channels['rc'][1] & channels['stable'][1], 'the channels must not share music'
    # The rc's notes and links are duplicated: the card draws the same two
    # lists the shared captions print, so a description repeats its picture.
    card = channels['rc'][0].decode()
    assert 'index .announcement 0' in card and 'range $i, $line := .announcement' in card and 'range .links' in card
    shared = (announcement_root / 'publish.yaml').read_text()
    assert shared.count('{{ range .links }}') == 2 and shared.count('{{ range .announcement }}') == 2
    # Channel copy is data: quotes, backslashes, blank lines, and a final line
    # without a newline must survive JSON encoding from any working directory.
    fixture = work / 'announcement'
    (fixture / 'rc').mkdir(parents=True)
    shutil.copyfile(announcement_root / 'notes.sh', fixture / 'notes.sh')
    text = 'A "quoted" saga \\ path\n\nLocks\tfirst in ${DISPAT_NEW_VERSION}'
    (fixture / 'rc/announcement.md').write_text(text)
    (fixture / 'rc/links.md').write_text('This rc: https://example.com/v${DISPAT_NEW_VERSION}\n\nGuide: https://example.com/next/')
    result = subprocess.run(['sh', str(fixture / 'notes.sh')], cwd=work,
                            env={**env, 'DISPAT_NEW_VERSION': '1.11.0-rc.1'},
                            capture_output=True, text=True, check=True)
    data = json.loads(result.stdout)
    assert data['announcement'] == text.replace('${DISPAT_NEW_VERSION}', '1.11.0-rc.1').splitlines()
    assert data['links'] == [{'label': 'This rc', 'url': 'https://example.com/v1.11.0-rc.1'},
                             {'label': 'Guide', 'url': 'https://example.com/next/'}]
    for broken in ('https://example.com/bare', 'Guide: http://example.com/', 'Guide: https://example.com/a b', ''):
        (fixture / 'rc/links.md').write_text(broken)
        result = subprocess.run(['sh', str(fixture / 'notes.sh')], cwd=work,
                                env={**env, 'DISPAT_NEW_VERSION': '1.11.0-rc.1'},
                                capture_output=True, text=True)
        assert result.returncode != 0 and not result.stdout, f'a bad links file must fail before rendering: {broken!r}'
    (fixture / 'rc/links.md').write_text('Guide: https://example.com/next/')
    (fixture / 'rc/announcement.md').unlink()
    result = subprocess.run(['sh', str(fixture / 'notes.sh')], cwd=work,
                            env={**env, 'DISPAT_NEW_VERSION': '1.11.0-rc.1'},
                            capture_output=True, text=True)
    assert result.returncode != 0 and not result.stdout, 'missing copy must fail before rendering'
print('announcement flow: channel copy, links and configuration, pinned installs, one publisher call, platform selection and no retries passed')
