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
