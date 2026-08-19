module github.com/yohimik/dispat/pkg/scanner

go 1.25

require (
	github.com/pelletier/go-toml/v2 v2.2.4
	golang.org/x/mod v0.29.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/kr/pretty v0.3.1 // indirect
	github.com/yohimik/dispat/pkg/manifest v1.1.1
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15 // indirect
)

// The proxy and the checksum database hold a different copy of this version,
// cached from a run that was rolled back before the tag was recreated; a
// proxy-mediated fetch of it either fails verification or serves the old bytes.
retract v1.0.0-rc.3
