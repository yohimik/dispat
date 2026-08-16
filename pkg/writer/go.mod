module github.com/yohimik/dispat/pkg/writer

go 1.25

require (
	github.com/yohimik/dispat/pkg/manifest v1.0.0-rc.4
	golang.org/x/mod v0.29.0
)

require github.com/pelletier/go-toml/v2 v2.2.4

// Retracted for the same reason as pkg/scanner's v1.0.0-rc.3: the proxy's
// first copy of this version came from a rolled-back run and differs from
// the tag that finally shipped under this name.
retract v1.0.0-rc.2
