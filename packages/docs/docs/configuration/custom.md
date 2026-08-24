# custom

Put data for your own tooling in the `custom` object. dispat checks everything else in the file against known keys to
catch typos. It carries `custom` without looking inside.

```yaml
custom:
  team: platform
  dashboard: https://grafana.example/d/releases
```

Add `custom` objects to spaces and package entries as needed. Unlike `env`, these objects do not merge. Each one
belongs only to the level where you write it.

Do not use a literal [`$ref`](./refs.md) key for your own data. dispat evaluates references inside `custom` exactly
like it does everywhere else. Writing `custom: {$ref: ./cfg/team.yaml}` pulls that file in, and so does a `$ref` nested
anywhere within your object.
