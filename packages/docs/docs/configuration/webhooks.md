# Webhooks

Use `webhooks` to send release events to a dashboard, chat bot, or deployment tracker. Events cover the start and end
of the run, package stages, and package outcomes. They save you from having to parse the
[JSON log stream](./README.md#log-levels).

```yaml
webhooks:
  - url: https://ci.example.com/hooks/dispat
    events: [release.started, release.finished]
  - name: deploy-tracker
    url: https://tracker.internal/dispat
    events: [package.published, package.failed]
    headers:
      - name: X-Api-Key
        value: $TRACKER_TOKEN
    secretEnv: DISPAT_WEBHOOK_SECRET
  - url: https://audit.example.com/events
```

Declare as many webhooks as you need. Each one subscribes independently, and several may subscribe to the same event;
each receives its own delivery.

**Webhook failures do not stop a release or change its exit code.** Deliveries run asynchronously. An unavailable,
slow, or rejecting endpoint produces a `W239` warning if delivery fails. Use
[script hooks](./spaces.md#stages-and-hooks) for checks that must be able to stop a release.

## Webhook options

| Key         | Type             | Required | Description                                                                                              |
|-------------|------------------|----------|------------------------------------------------------------------------------------------------------------|
| `url`       | string           | yes      | The endpoint the event payloads are sent to. The scheme must be `http` or `https`.                       |
| `method`    | string           | no       | The HTTP method: `POST` (default), `PUT`, or `PATCH`.                                                    |
| `events`    | array of strings | no       | The events this webhook subscribes to. A single name may be written as a bare string. `*` matches every event, and a family pattern such as `package.*` matches the whole family. An empty or absent list subscribes to every event. |
| `headers`   | array of objects | no       | Extra request headers, each a `{name, value}` object. Values may reference environment variables (`$NAME`, `${NAME}`), resolved once per run, so a token stays out of the file. |
| `secretEnv` | string           | no       | The name of an environment variable holding a signing secret. When set, every delivery carries an `X-Dispat-Signature` header. See [Verifying deliveries](#verifying-deliveries). |
| `env`       | string           | no       | A condition gating the webhook on the process environment, in the grammar [`dispat if`](../cli/if.md) uses: `NAME`, `!NAME`, `NAME=value`, `NAME!=value`, `NAME~glob`, `NAME!~glob`. `env: CI=true` keeps a webhook silent on every laptop and active on the runner. Evaluated once per run; unmet means disabled, exactly as if the webhook were not declared. |
| `format`    | string           | no       | A template replacing the default JSON payload. See [A custom payload format](#a-custom-payload-format). |
| `timeout`   | int              | no       | Seconds one delivery attempt may take. The default is `10`.                                              |
| `name`      | string           | no       | The label log lines call the webhook by. The default is the URL's host. When set, it must be unique.     |

`headers` is a list of objects rather than a map so header names keep their case exactly as you write them.

## The events

Webhooks belong to `dispat release`. The step commands fire none of them, for the same reason they fire no
[run-level hooks](./run-hooks.md#they-belong-to-dispat-release). Events begin once the run is committed to execute: a
run refused before that (a blocked plan, failed verification, a failed `run.beforeAll`) delivers nothing.

| Event               | Fires                                                                                             |
|---------------------|----------------------------------------------------------------------------------------------------|
| `release.started`   | Once, when the task graph is about to start, carrying the whole plan.                             |
| `release.finished`  | Once, when the run has settled, carrying the outcome counts and every package's status.           |
| `stage.started`     | When one package's `sign`, `version` or `propagate`, `syncLock`, `build`, `test`, `publish` or `announce` stage, or a [stage the flow defines](./spaces.md#stages-of-your-own), starts. |
| `stage.succeeded`   | When that stage completes. A stage with nothing to run, such as a build with no `build` entry, still fires both; a stage after the records that failed fires `stage.started` alone and only warns. |
| `package.published` | When one package's publish frame completes, carrying the release tag.                             |
| `package.failed`    | When one package fails, naming the failed stage and the error.                                    |
| `package.skipped`   | When one package is skipped because a dependency failed, with `code: "W194"` and `blockedBy`.     |
| `package.cancelled` | When an interrupted run stops a package before or during its work.                                |
| `script.progress`   | When a stage script raises it through [`dispat trigger progress`](../cli/trigger.md), with a 0 to 100 value and an optional message. |
| `script.<word>`     | When a stage script raises its own event through [`dispat trigger <word>`](../cli/trigger.md): the family is open-ended, and a subscription may name any word a trigger can say. |

A stage is observed only for a package that has one: the `build` stage for every package, the `test`, `publish` and
`announce` stages for a package whose flow configures the stage or one of its hooks, the `sign` stage for one whose space configures
[`autoSign`](./autosign.md) or a sign flow entry, and a stage the flow defines under its own name. A package with no
publish stage reports `package.published` once the stages before its record completed. A stage
[after the records](./spaces.md#the-order-of-the-stages) reports its events after `package.published`, as the
announce does, and a stage that failed without failing the package reports no `stage.succeeded`. The `stage.*` events
and a `package.failed` event carry a `label` field when the flow gave the stage one. The version stage is named after
the key that
configured it: `propagate` under `flow.propagate` or `autoPropagate`, `version` under the version keys, and a
`failedStage` carries the same name (see [Two names for one block](./autoversion.md#two-names-for-one-block)). The
`release.` / `stage.` / `package.` prefixes carry dispat's own events and only those; the `script.` prefix carries what
a script said, so a listener tells the two apart by the prefix alone. Other prefixes are reserved for later commands.

A stage script raises its own events between the stage brackets with [`dispat trigger`](../cli/trigger.md):

```yaml
scripts:
  build:
    - npm ci        && dispat trigger progress 40 dependencies installed
    - npm run build && dispat trigger progress 100 built
  publish:
    - ./release.sh  && dispat trigger deployed version is live
```

Each invocation delivers one event carrying the message (and, for `progress`, the value) beside the package, stage,
and version of the script that raised it.

## Levels and overrides

You can set `webhooks` at the root, on a space, on a package, or in an in-folder configuration file. The
[nearest list wins](./packages.md#the-override-ladder), replacing the inherited list completely. A package with its
own list sends package events only to that list. Set `webhooks: []` to disable them for a level.

```yaml
webhooks:
  - url: https://audit.example.com/events        # every package reports here...
spaces:
  services:
    packages:
      payments:
        webhooks:
          - url: https://pci.example.com/hooks    # ...except payments, which reports here alone
      internal-tool:
        webhooks: []                              # ...and this one, which reports nowhere
```

The run events (`release.started`, `release.finished`) always go to the top-level list alone. A webhook inherited by
several packages keeps one shared delivery queue and order.

## A custom payload format

`format` replaces the default JSON payload with a rendered template, for endpoints that want their own shape:

```yaml
webhooks:
  - url: https://hooks.slack.com/services/T000/B000/XXXX
    events: [package.published, package.failed]
    format: '{"text": "dispat: {package} {version} {event}"}'
```

A `{field}` token (letters only) is replaced by that field of the event, and every other byte is literal, so a
template may itself be JSON. The fields are the payload's own scalar names: `event`, `timestamp`, `package`, `stage`,
`label`, `version`, `previousVersion`, `channel`, `tag`, `status`, `failedStage`, `error`, `code`, `blockedBy`,
`progress`, `message`, `root`, `published`, `failed`, `skipped`, `cancelled`, `role`, `node`, and `worker`. A token
naming anything else is refused at load.

Substituted values are escaped for a JSON string position, so a template embedding `{error}` inside its quotes stays
valid JSON whatever the error text carries. The delivery still carries the `X-Dispat-Event` and `X-Dispat-Delivery`
headers, and the [signature](#verifying-deliveries) is computed over the body actually sent. A field the event does
not carry renders as empty; the list-valued `packages` field has no token, because a one-line template has no one
rendering for a list.

## The payload

Every delivery is a JSON object naming its event, stamped when it happened. The field names are the JSON log stream's
own, so a consumer of either reads the same vocabulary. Fields that do not apply are absent.

A `package.published` delivery:

```json
{
  "event": "package.published",
  "timestamp": "2026-08-27T10:15:04.113Z",
  "package": "api",
  "version": "1.4.0",
  "previousVersion": "1.3.2",
  "channel": "beta",
  "tag": "api@1.4.0-beta.1",
  "status": "published"
}
```

A `package.failed` delivery carries `failedStage` and `error` instead of `tag`; a `package.skipped` delivery carries
`code` and `blockedBy`. A `stage.started` or `stage.succeeded` delivery names its `stage` beside the same package
fields. `channel` is absent on a stable release.

A run with an [`execution`](./execution.md) object names the process every delivery was sent from: `role`
(`orchestrator` or `worker`) and `node`, that process's node name. A stage the run placed on a worker is still
reported by the orchestrator, which names the node that ran it in `worker`. A run with no `execution` object sends
none of the three.

A `release.finished` delivery:

```json
{
  "event": "release.finished",
  "timestamp": "2026-08-27T10:15:09.870Z",
  "root": "/work/monorepo",
  "status": "failed",
  "published": 1,
  "failed": 1,
  "packages": [
    {"package": "core", "version": "2.0.0", "previousVersion": "1.9.1", "status": "published"},
    {"package": "api", "version": "1.4.0", "previousVersion": "1.3.2", "status": "failed"}
  ]
}
```

`status` is the run's own word: `succeeded`, `failed`, or `interrupted`. The counts (`published`, `failed`,
`skipped`, `cancelled`) appear when non-zero. `release.started` carries the same `packages` list without statuses: the
plan as it stood before anything ran.

Beside the body, every request carries `X-Dispat-Event` naming the event and `X-Dispat-Delivery` holding an opaque id
that is unique per delivery and stable across its retries, so a receiver can deduplicate.

## Delivery semantics

**Order.** Within one dispat process, each webhook has its own delivery lane. Events sent through that lane retain
their order, and a slow endpoint never delays another endpoint. Events from concurrently running packages can
interleave.

A `dispat trigger` command runs in a separate process with its own delivery lane. Its progress event can arrive before
the parent process's queued `stage.started` event. Sequential trigger commands finish their bounded delivery flush
before returning, but there is no global HTTP arrival order across processes. Use the event's package, stage, and
payload to associate it with the work it describes.

**Retries.** A transport error, a `429`, or a `5xx` answer is retried up to three attempts with a short growing delay.
Any other non-2xx answer is not: a `404` or a `401` will not improve on a second attempt. Each attempt is bounded by
the webhook's `timeout`.

**Failure is a warning.** A delivery whose retries are exhausted, or one dropped because the queue is full, warns with
`code: "W239"` naming the webhook and the event. The release is untouched either way.

**The flush.** When the run ends, whether it succeeds, fails, or is interrupted, dispat waits briefly for the
deliveries still in flight, bounded by a fixed deadline, and abandons the rest with a warning. An interrupt is the
outcome a listener most wants to hear about, so the `package.cancelled` events and the closing `release.finished` are
delivered before the process exits.

## Verifying deliveries

Set `secretEnv` to the name of an environment variable and every delivery carries an `X-Dispat-Signature` header:
`sha256=` followed by the hex HMAC-SHA256 of the request body, keyed with the variable's value. This is the same
convention as GitHub's `X-Hub-Signature-256`, so existing verification code works unchanged:

```python
import hashlib, hmac

def verify(secret: bytes, body: bytes, signature: str) -> bool:
    digest = hmac.new(secret, body, hashlib.sha256).hexdigest()
    return hmac.compare_digest("sha256=" + digest, signature)
```

The secret itself never appears in the config file, is never logged, and the signature header cannot be overridden by
a configured header. A `secretEnv` naming an unset or empty variable warns once and delivers unsigned rather than
refusing the run.
