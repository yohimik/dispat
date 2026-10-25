# custom

Use `custom` for settings that belong to your own tooling. dispat accepts any keys inside this object; elsewhere, it
rejects unknown keys to catch configuration typos.

```yaml
custom:
  team: platform
  dashboard: https://grafana.example/d/releases
```

You can add `custom` to the root, a space, or a package. Each object belongs to that level only; the objects do not
merge or inherit like `env` maps.

Do not use a literal [`$ref`](./refs.md) key for your own data. dispat evaluates references inside `custom` exactly
like it does everywhere else. Writing `custom: {$ref: ./cfg/team.yaml}` pulls that file in, and so does a `$ref` nested
anywhere within your object.
