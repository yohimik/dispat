#!/usr/bin/env python3
"""Exercise announcement decisions with a fake publisher; never contact a platform."""
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
    out.write(json.dumps({'args':sys.argv[1:], 'data':json.load(sys.stdin)})+'\\n')
sys.exit(int(os.environ.get('ANNOUNCE_TEST_EXIT', '0')))
''')
    fake.chmod(0o755)
    log = work / 'calls.jsonl'
    env = {k:v for k,v in os.environ.items() if not k.startswith(('ANNOUNCE_', 'CRIER_', 'DISPAT_'))}
    env.update(DISPAT_STAGE='announce', DISPAT_NEW_VERSION='1.8.0',
               DISPAT_FEATURES='Aqua support', DISPAT_FIXES='Lock ownership',
               CRIER_PUBLISH_INSTAGRAM_TOKEN='test', CRIER_PUBLISH_INSTAGRAM_USER_ID='test',
               CRIER_PUBLISH_LINKEDIN_TOKEN='test', CRIER_PUBLISH_LINKEDIN_AUTHOR_URN='test',
               CRIER_STAGE_MODE='url', ANNOUNCE_CRIER_BIN=str(fake), ANNOUNCE_TEST_LOG=str(log))

    def run(**overrides):
        log.write_text('')
        result = subprocess.run(['sh', 'announce/announce.sh'], cwd=root,
                                env={**env, **overrides}, capture_output=True, text=True)
        return result.returncode, [json.loads(line) for line in log.read_text().splitlines()]

    code, calls = run()
    assert code == 0 and len(calls) == 2
    for call, cap in zip(calls, ['10', '20']):
        args = call['args']
        assert '--render-video-enabled=false' in args
        assert args[args.index('--render-pages-max')+1] == cap
        assert not any('lead-video' in arg or arg == '--publish-input' or arg == '--publish-instagram-story' for arg in args)
        assert call['data']['sections'][0]['items'] == ['Aqua support']
    code, calls = run(ANNOUNCE_INSTAGRAM_COVER_ONLY='1')
    assert code == 0 and len(calls) == 2
    assert calls[0]['data']['coveronly'] and not calls[1]['data'].get('coveronly')
    assert calls[0]['data']['sections'][1]['items'] == ['Lock ownership']
    code, calls = run(ANNOUNCE_TEST_EXIT='1')
    assert code == 0 and len(calls) == 2, 'failed publishing must not create fallback duplicate posts'
    code, calls = run(ANNOUNCE_ONLY='linkedin', ANNOUNCE_TEST_EXIT='1', ANNOUNCE_LINKEDIN_COVER_ONLY='1')
    assert code == 1 and len(calls) == 1 and calls[0]['data']['coveronly']
    code, calls = run(DISPAT_STAGE='run:announce')
    assert code == 0 and calls == []
print('announcement flow: one post per platform, cover-only captions, guards and no retries passed')
