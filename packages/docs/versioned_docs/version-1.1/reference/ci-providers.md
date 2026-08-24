# The release job on other providers

This page shows the same release job written for GitLab CI, CircleCI, Jenkins, Buildkite, and Azure Pipelines. It
highlights only the parts that differ.

dispat needs four things from a CI provider: `git`, a POSIX shell, the full repository history, and credentials for
whatever your stages push. Everything else below is one provider's spelling of those requirements. Read
[dispat in CI](./ci.md) to get the binary onto the runner, and [Pipeline patterns](./pipelines.md) to see what to run
once it is there.

## What every provider has to get right

**The clone must not be shallow.** dispat reads tags and commit ranges. A truncated history makes every version it
computes wrong, so it refuses to guess and stops with `E196` instead. Most providers clone shallowly by default, so you
must change this setting.

**Tags must come with it.** A baseline is a tag. A clone without tags looks like a repository that has never released.

**Something must be able to push.** Tags and the release commit go back to the remote. Change your checkout token if it
is read-only.

**Concurrency is already handled.** A run claims the repository with a [release lock](./releasing/release-lock.md) tag
on the remote before planning. The second pipeline stops with a clear message if two pipelines release at once. You do
not need the provider's own concurrency groups for correctness.

Run this check once in your job before calling dispat. It catches the first two problems on any provider:

```sh
git rev-parse --is-shallow-repository   # must print false
git tag | wc -l                         # must not be 0 in a repository that has released
```

## GitLab CI

Set `GIT_DEPTH: 0` because GitLab clones the last 20 commits by default. Put a project access token with
`write_repository` in a masked variable. The job token cannot push, so this masked token becomes the remote's
credentials.

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

Turn off the GitHub releases recorder. Let the changelog and the tags be the record instead:

```json title="dispat.json"
{"github": {"enabled": false}}
```

Call `release-cli` from a [`postPublish` hook](../configuration/spaces.md) to create GitLab releases. dispat already
sets `$DISPAT_TAG` and `$DISPAT_NEW_VERSION` in that environment.

## CircleCI

You usually have nothing to change because the built-in `checkout` step brings the full history and the tags. Add the
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

Add a deploy key or a user key with write access in your project's SSH settings. The checkout step uses this key to
push back to the remote.

## Jenkins

Specify your clone options because the Git plugin's defaults depend on how you set up the job. `shallow: false` and
`noTags: false` are the two options that matter.

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

A Jenkins agent often keeps its workspace between builds, which is fine because dispat reads the repository rather than
caching anything. A leftover detached HEAD releases from whatever was checked out last, so make sure the workspace is
on the correct branch. Use [`run.allowBranch`](../configuration/run-hooks.md#the-branch-guard) to turn that mistake
into a refusal.

## Buildkite

Buildkite agents clone fully unless you configure them otherwise. Remove `--depth` from `BUILDKITE_GIT_CLONE_FLAGS` for
this pipeline if your agent sets it.

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

Secrets come from the agent's environment hook or a secrets plugin. Set `propagate-environment: true` to pass them into
the container and your stage scripts.

## Azure Pipelines

Set `fetchDepth: 0` for the history and `fetchTags: true` for the baselines. Set `persistCredentials: true` so the push
has credentials.

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

Give the build service identity "Contribute" access on the repository. This is the Azure spelling of a token that can
push.

## What does not change

Everything above stops at the shell prompt. The release is identical on every provider once dispat runs. You get the
same plan, the same order, the same tags, and the same changelog.

Two features are the exception, and both are opt-in:

| Feature | Outside GitHub |
|---------|----------------|
| [GitHub releases](../configuration/records.md#github) | Set `github.enabled: false`, or point `github.apiUrl` at a GitHub Enterprise instance. Set `github.owner` and `github.repo` to use GitHub releases from a mirrored repository. |
| [The composite action](./ci.md#the-github-action) | Use a [container image](./ci.md#the-container-images) or the [install script](../getting-started.md#install) instead. |

## See also

- Read [dispat in CI](./ci.md) for the three ways to get the binary onto a runner.
- Read [Pipeline patterns](./pipelines.md) to gate jobs on the plan and on what changed.
- Read [The release lock](./releasing/release-lock.md) to see what happens when two pipelines release at once.
