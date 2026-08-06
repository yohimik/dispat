module github.com/yohimik/dispat/tests/integration

go 1.26

require (
	github.com/stretchr/testify v1.11.1
	github.com/yohimik/dispat/pkg/models v0.0.0-00010101000000-000000000000
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/yohimik/dispat/pkg/ccme v0.0.0-00010101000000-000000000000 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/yohimik/dispat/pkg/ccme => ../../pkg/ccme
	github.com/yohimik/dispat/pkg/models => ../../pkg/models
)
