#!/usr/bin/env python3
"""Build the stable announcement's data document from a dispat release.

A stable release announces the notes dispat generated for it, and this is where
they become the document stable/crier.yaml reads on standard input. A release
candidate does not come through here: its announcement is written by hand in
rc/notes.yaml and takes only the version from the environment.

It reads the release-notes variables dispat gives every stage (documented in
reference/environment.md: one entry per line, in history order, empty rather
than unset) and prints one JSON object, so it can be run and checked without a
release, a network, or a binary:

    DISPAT_NEW_VERSION=1.2.3 DISPAT_FEATURES="a
    b" python3 services/dispat/announce/notes.py
"""
import json
import os

# How many entries a section shows before it says how many are left. The card
# paginates, so this is a ceiling on absurdity rather than a design constraint,
# and it keeps a real release under render.pages-max, which refuses the render
# outright rather than truncating it.
limit = int(os.environ.get('ANNOUNCE_MAX_ITEMS') or 20)
version = os.environ.get('DISPAT_NEW_VERSION') or 'dev'

# The four groups, in the order the changelog and the GitHub release use. PICKS
# UP is the dependencies section: on a monorepo a provider's release rewriting
# its consumers is half of what a run did, so the card says it. An empty group
# is left out, because a heading with nothing under it looks broken.
sections = []
for label, variable in (('BREAKING', 'DISPAT_BREAKING_CHANGES'), ('FEATURES', 'DISPAT_FEATURES'),
                        ('FIXES', 'DISPAT_FIXES'), ('PICKS UP', 'DISPAT_DEPENDENCIES')):
    items = [line.replace('\r', '') for line in os.environ.get(variable, '').split('\n') if line.strip()]
    if items:
        sections.append({'label': label, 'items': items[:limit], 'more': max(0, len(items) - limit)})

document = {
    'version': version,
    'sections': sections,
    # The three routes the README documents, each naming the announced version:
    # the alias tag dispat writes for its own CLI is v<version>, so the raw URL
    # resolves to the installer that shipped with it.
    'install': [
        {'label': 'curl', 'command': 'curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/'
                                     f'v{version}/install.sh | DISPAT_VERSION={version} sh'},
        {'label': 'self-update', 'command': f'dispat self-update --release {version}'},
        {'label': 'action', 'command': f'{{uses: yohimik/dispat@v1, with: {{version: {version}}}}}'},
    ],
}
# Optional render controls; the sections stay in the data for the captions.
if os.environ.get('ANNOUNCE_NO_COVER'):
    document['nocover'] = True
if os.environ.get('ANNOUNCE_COVER_ONLY'):
    document['coveronly'] = True
print(json.dumps(document, ensure_ascii=False))
