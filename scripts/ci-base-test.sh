#!/bin/sh
set -eu
helper=$(cd "$(dirname "$0")" && pwd)/ci-base.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
cd "$tmp"
git init -q
git config user.name test
git config user.email test@example.invalid
mkdir -p pkg/a scripts
cp "$helper" scripts/ci-base.sh
printf 'one\n' > pkg/a/a.go
git add . && git commit -qm first
first=$(git rev-parse HEAD)
printf 'two\n' >> pkg/a/a.go
git commit -qam second
second=$(git rev-parse HEAD)
printf 'three\n' >> pkg/a/a.go
git commit -qam third

assert() { [ "$1" = "$2" ] || { echo "$3: got $1, want $2" >&2; exit 1; }; }
got=$(GITHUB_EVENT_NAME=push GITHUB_EVENT_BEFORE=$first CI_REPOSITORY=$tmp sh "$helper")
assert "$got" "$first" 'multi-commit push uses event before'
got=$(GITHUB_EVENT_NAME=pull_request CI_REPOSITORY=$tmp sh "$helper")
assert "$got" all 'pull request without tested merge falls back to all'
got=$(GITHUB_EVENT_NAME=push GITHUB_EVENT_BEFORE=0000000000000000000000000000000000000000 CI_REPOSITORY=$tmp sh "$helper")
assert "$got" all 'missing push base falls back to all'

printf '# shared\n' >> dispat.yaml
git add dispat.yaml && git commit -qm shared
printf 'after shared\n' >> pkg/a/a.go
git commit -qam after-shared
got=$(GITHUB_EVENT_NAME=push GITHUB_EVENT_BEFORE=$second CI_REPOSITORY=$tmp sh "$helper")
assert "$got" all 'shared infrastructure anywhere in the range forces all'

git checkout -qb feature "$first"
printf 'feature\n' > pkg/a/feature.go
git add pkg/a/feature.go && git commit -qm feature
git checkout -qb merge-target "$second"
git merge -q --no-ff -m 'tested merge' feature
got=$(GITHUB_EVENT_NAME=pull_request CI_REPOSITORY=$tmp sh "$helper")
assert "$got" 'HEAD^1' 'tested pull request merge uses first parent'
got=$(GITHUB_EVENT_NAME=push GITHUB_EVENT_BEFORE=$second CI_REPOSITORY=$tmp sh "$helper")
assert "$got" "$second" 'single merge push uses previous revision'
mkdir -p packages/docs/demo/fixtures
printf '{}\n' > packages/docs/demo/fixtures/story.json
git add packages/docs/demo/fixtures && git commit -qm fixture
got=$(GITHUB_EVENT_NAME=push GITHUB_EVENT_BEFORE=HEAD^ CI_REPOSITORY=$tmp sh "$helper")
assert "$got" all 'demo fixture changes run integration gates'
printf '# landing source\n' >> README.md
git add README.md && git commit -qm root-readme
got=$(GITHUB_EVENT_NAME=push GITHUB_EVENT_BEFORE=HEAD^ CI_REPOSITORY=$tmp sh "$helper")
assert "$got" all 'root README changes rebuild the docs-derived landing through the full gate'
mkdir -p specs/ccme-spec
printf '# diagnostic registry\n' > specs/ccme-spec/SPEC.md
git add specs/ccme-spec/SPEC.md && git commit -qm specification
got=$(GITHUB_EVENT_NAME=push GITHUB_EVENT_BEFORE=HEAD^ CI_REPOSITORY=$tmp sh "$helper")
assert "$got" all 'specification changes run the CLI diagnostic-reference test'
mkdir -p .aqua
printf 'packages: []\n' > .aqua/aqua.yaml
git add .aqua/aqua.yaml && git commit -qm tool-manifest
got=$(GITHUB_EVENT_NAME=push GITHUB_EVENT_BEFORE=HEAD^ CI_REPOSITORY=$tmp sh "$helper")
assert "$got" all 'Aqua tool changes run the toolchain and release build gates'
printf 'ci base scenarios passed\n'
