# custom

`custom` is a free-form object dispat never reads. Everything else in the file is checked against the known keys, and an
unrecognised one is an error that catches typos. `custom` is the exception: put whatever your own tooling needs in it
and dispat will carry it without looking inside.

```yaml
custom:
  team: platform
  dashboard: https://grafana.example/d/releases
```

Spaces and package entries have their own `custom` objects. Unlike `env`, nothing merges: each one belongs to the level
that wrote it.

One thing dispat does look at inside `custom`: a [`$ref`](./refs.md) means there what it means everywhere else, so
`custom: {$ref: ./cfg/team.yaml}` pulls that file in, and so does a `$ref` nested anywhere within. There is no
exception for `custom`, which also means data of your own that happens to use a `$ref` key is read as a reference.
