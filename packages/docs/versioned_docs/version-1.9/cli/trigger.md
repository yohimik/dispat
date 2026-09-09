# The trigger command

Run `dispat trigger <event> [message]` to deliver one script-raised event to the configured
[webhooks](../configuration/webhooks.md). The event is one word (a letter, then letters, digits, dashes, or
underscores), delivered as `script.<word>`, so a subscription tells dispat's own events apart from what a script said
by the prefix alone. Everything after the event is the event's free-text message:

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

It exists for a stage script saying something between the `stage.started` and `stage.succeeded` events the release
emits on its own, at the moments only the script knows about.

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

The command exits `0` whatever the endpoints think of the delivery. An endpoint that cannot be reached is a `W239`
warning, exactly as it is for the release's own events, because a build script must not be able to fail the build by
reporting. With no webhooks configured the command does nothing and exits `0`. The one thing it refuses is a usage
mistake: a malformed event word, or a progress value that is not a whole number between 0 and 100, exits `2` before
any config is read.

Deliveries drain before the command returns, bounded by the webhook's own `timeout` and the flush deadline, so a slow
endpoint delays the script by at most that bound and a fast one costs it almost nothing.
