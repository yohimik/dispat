# The trigger command

Use `dispat trigger <event> [message]` inside a stage script to report progress or another milestone to your
[webhooks](../configuration/webhooks.md).

Event names start with a letter and may then contain letters, digits, dashes, or underscores. A name such as
`deployed` is delivered as `script.deployed`, keeping script events distinct from dispat's built-in events. The text
after the event name becomes its message:

```yaml
scripts:
  build:
    - dispat trigger progress 0 starting
    - npm ci        && dispat trigger progress 40 dependencies installed
    - npm run build && dispat trigger progress 100 built
  publish:
    - ./release.sh  && dispat trigger deployed version is live
    - ./smoke.sh    && dispat trigger smoke-passed all green
```

`progress` is the one typed event: its first argument is a whole number from 0 to 100, and the message follows it.
Every other word is yours to define, and a webhook subscribes to it by exact name (`script.deployed`) or by the family
(`script.*`).

Use these events for milestones between the `stage.started` and `stage.succeeded` events dispat sends automatically.

## The environment names the sender

Invoked from a stage script, the command reads `DISPAT_PACKAGE`, `DISPAT_STAGE`, `DISPAT_NEW_VERSION`, and
`DISPAT_CHANNEL` from the [environment the stage exported](../reference/environment.md), so the delivery attributes
itself to the package and stage that raised it and routes to that package's
[effective webhook list](../configuration/webhooks.md#levels-and-overrides):

```json
{
  "event": "script.deployed",
  "timestamp": "2026-08-27T10:15:07.221Z",
  "package": "api",
  "stage": "publish",
  "version": "1.4.0",
  "message": "version is live"
}
```

A `script.progress` payload carries `progress` beside the message. Run by hand outside a release, the package fields
are simply absent: the event still delivers, to the top-level list alone.

## It cannot fail the stage

A delivery failure logs `W239` and still exits `0`, so a reporting problem does not fail the stage. With no webhooks
configured, the command does nothing and exits `0`.

Invalid arguments do fail: a malformed event name or a progress value outside the whole numbers 0–100 exits `2`
before configuration is read.

Deliveries drain before the command returns, bounded by the webhook's own `timeout` and the flush deadline, so a slow
endpoint delays the script by at most that bound and a fast one costs it almost nothing.
