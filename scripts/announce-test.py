#!/usr/bin/env python3
"""Check the release's single Crier invocation without contacting any platform."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
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

    def run(**overrides):
        log.write_text('')
        result = subprocess.run(['sh', 'announce/announce.sh'], cwd=root,
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
print('announcement flow: one publisher call, changelog data, platform selection and no retries passed')
