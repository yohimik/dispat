# The release job on other providers

The same release job written for GitLab CI, CircleCI, Jenkins, Buildkite and Azure Pipelines, with only the parts that
actually differ spelled out.

dispat needs remarkably little from a CI provider: `git`, a POSIX shell, the full history of the repository, and
credentials for whatever your stages push. Everything else on this page is one provider's spelling of those four
things. [dispat in CI](./ci.md) covers getting the binary onto the runner, and
[Pipeline patterns](./pipelines.md) covers what to run once it is there.

## What every provider has to get right

**The clone must not be shallow.** dispat reads tags and commit ranges, so a truncated history makes every version it
computes wrong. It refuses to guess and stops with `E196` instead. Most providers clone shallowly by default to save
time, so this is the setting you will actually have to change.

**Tags must come with it.** A baseline is a tag. A clone without tags looks like a repository that has never released.

**Something must be able to push.** Tags, and the release commit if you use one, go back to the remote. The token
that checks the code out is often read-only, and that is the second thing to change.

**Concurrency is already handled.** Before planning, a run claims the repository with a
[release lock](./releasing/release-lock.md) tag on the remote. Two pipelines releasing at once means the second one
stops with a clear message rather than racing the first, so you do not need the provider's own concurrency groups for
correctness.

One check catches the first two problems on any provider. Run it once in the job, before dispat:

```sh
git rev-parse --is-shallow-repository   # must print false
git tag | wc -l                         # must not be 0 in a repository that has released
```

## GitLab CI

GitLab clones the last 20 commits by default, so `GIT_DEPTH: 0` is required. The job token cannot push, so a project
access token with `write_repository` goes in a masked variable and becomes the remote's credentials.

```yaml title=".gitlab-ci.yml"
release:
  image: yohimik/dispat-alpine:latest
  rules:
    - if: $CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH
      when: manual
  variables:
    GIT_DEPTH: 0                       # full history; the default is 20 commits
    GIT_FETCH_EXTRA_FLAGS: --tags
  before_script:
    - git remote set-url origin "https://oauth2:${RELEASE_TOKEN}@${CI_SERVER_HOST}/${CI_PROJECT_PATH}.git"
  script:
    - dispat --log-format json
```

GitLab has no GitHub releases, so turn that recorder off and let the changelog and the tags be the record:

```json title="dispat.json"
{"github": {"enabled": false}}
```

To create GitLab releases as well, call `release-cli` from a
[`postPublish` hook](../configuration/spaces.md), where `$DISPAT_TAG` and `$DISPAT_NEW_VERSION` are already set.

## CircleCI

The built-in `checkout` step brings the full history and the tags, so there is usually nothing to change. Add the
preflight check above if your configuration uses a custom clone.

```yaml title=".circleci/config.yml"
version: 2.1

jobs:
  release:
    docker:
      - image: yohimik/dispat-alpine:latest
    steps:
      - checkout
      - run: dispat --log-format json

workflows:
  release:
    jobs:
      - release:
          filters:
            branches:
              only: main
```

Pushing back needs a deploy key or a user key with write access, added in the project's SSH settings. The checkout
step then uses it for the push as well.

## Jenkins

The Git plugin's defaults depend on how the job was set up, which is why the clone options are spelled out here.
`shallow: false` and `noTags: false` are the two that matter.

```groovy title="Jenkinsfile"
pipeline {
  agent { docker { image 'yohimik/dispat-alpine:latest' } }

  stages {
    stage('checkout') {
      steps {
        checkout([
          $class: 'GitSCM',
          branches: [[name: '*/main']],
          extensions: [[$class: 'CloneOption', shallow: false, noTags: false, depth: 0]],
          userRemoteConfigs: [[url: env.GIT_URL, credentialsId: 'release-token']]
        ])
      }
    }
    stage('release') {
      steps {
        withCredentials([string(credentialsId: 'npm-token', variable: 'NPM_TOKEN')]) {
          sh 'dispat --log-format json'
        }
      }
    }
  }
}
```

A Jenkins agent often keeps its workspace between builds. That is fine for dispat, which reads the repository rather
than caching anything, but make sure the workspace is on the branch you meant: a leftover detached HEAD releases from
whatever was checked out last. [`run.allowBranch`](../configuration/run-hooks.md#the-branch-guard) turns that into a
refusal.

## Buildkite

Buildkite agents clone fully unless someone configured otherwise. If your agent sets `BUILDKITE_GIT_CLONE_FLAGS` with
a `--depth`, remove it for this pipeline.

```yaml title=".buildkite/pipeline.yml"
steps:
  - label: ":rocket: release"
    branches: main
    command: dispat --log-format json
    plugins:
      - docker#v5.11.0:
          image: yohimik/dispat-alpine:latest
          propagate-environment: true
```

Secrets come from the agent's environment hook or a secrets plugin. `propagate-environment: true` is what passes them
into the container, and therefore into your stage scripts.

## Azure Pipelines

`fetchDepth: 0` for the history, `fetchTags: true` for the baselines, and `persistCredentials: true` so the push has
something to push with.

```yaml title="azure-pipelines.yml"
trigger: none

pool:
  vmImage: ubuntu-latest

steps:
  - checkout: self
    fetchDepth: 0
    fetchTags: true
    persistCredentials: true

  - script: curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
    displayName: Install dispat

  - script: dispat --log-format json
    displayName: Release
    env:
      NPM_TOKEN: $(NPM_TOKEN)
```

The build service identity needs "Contribute" on the repository, which is the Azure spelling of a token that can
push.

## What does not change

Everything above stops at the shell prompt. Once dispat runs, the release is identical on every provider: the same
plan, the same order, the same tags, the same changelog. That is the point of a stage being a shell command.

Two features are the exception, and both are opt-in:

| Feature | Outside GitHub |
|---------|----------------|
| [GitHub releases](../configuration/records.md#github) | Set `github.enabled: false`, or point `github.apiUrl` at a GitHub Enterprise instance. A mirrored repository can still use it by setting `github.owner` and `github.repo`. |
| [The composite action](./ci.md#the-github-action) | Use a [container image](./ci.md#the-container-images) or the [install script](../getting-started.md#install) instead. |

## See also

- [dispat in CI](./ci.md) for the three ways to get the binary onto a runner.
- [Pipeline patterns](./pipelines.md) for gating jobs on the plan and on what changed.
- [The release lock](./releasing/release-lock.md) for what happens when two pipelines release at once.
