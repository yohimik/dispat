package ccme_test

import (
	"fmt"

	"github.com/yohimik/dispat/pkg/ccme"
)

func ExampleParser_ParseSubject() {
	p := ccme.DefaultParser()

	res, err := p.ParseSubject("feat(@acme/core)^^minor%beta!: streaming reader")
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	u := res.Units[0]
	fmt.Println("type:       ", u.Header.Type)
	fmt.Println("scopes:     ", u.Scopes())
	fmt.Println("breaking:   ", u.Breaking)
	fmt.Println("bump:       ", u.Bump)
	fmt.Println("propagate:  ", u.Directives.Propagate)
	fmt.Println("depth:      ", u.Directives.Depth)
	fmt.Println("channel:    ", u.Directives.Channel)
	fmt.Println("description:", u.Header.Description)

	// Output:
	// type:        feat
	// scopes:      @acme/core
	// breaking:    true
	// bump:        major
	// propagate:   minor
	// depth:       all
	// channel:     beta
	// description: streaming reader
}

func ExampleParser_Parse() {
	const message = `feat(@acme/api): add cursor pagination

---

fix(@acme/api): reject negative page sizes

---

docs(docs-site): document pagination`

	p := ccme.DefaultParser()
	res, _ := p.Parse(message)

	for _, u := range res.ValidUnits() {
		fmt.Printf("%d: %-5s %-12s %s\n", u.Index, u.Header.Type, u.Scopes(), u.Bump)
	}
	fmt.Println("message bump:", res.Bump())

	// Output:
	// 0: feat  @acme/api    minor
	// 1: fix   @acme/api    patch
	// 2: docs  docs-site    none
	// message bump: minor
}

func ExampleParser_Parse_diagnostics() {
	p := ccme.DefaultParser()

	res, err := p.Parse("feat(core)^^minor+2: broken directive")
	fmt.Println("err != nil:", err != nil)
	for _, d := range res.Diagnostics {
		fmt.Printf("%s %s at %s\n", d.Severity, d.Code, d.Position)
	}

	// Output:
	// err != nil: true
	// error E113 at 1:18
}

func ExampleNewParser() {
	types := ccme.DefaultTypes()
	types["deps"] = ccme.BumpPatch

	p, err := ccme.NewParser(ccme.Config{
		Separator:   "%%%",
		StrictTypes: true,
		Types:       types,
		Propagation: ccme.PropagationConfig{Depth: ccme.DepthAll},
	})
	if err != nil {
		fmt.Println("config error:", err)
		return
	}

	res, err := p.Parse("deps(core): bump lockfile\n%%%\nfix(cli): guard nil")
	fmt.Println("units:", len(res.Units), "err:", err)
	fmt.Println("unit 0 bump:", res.Units[0].Bump)
	fmt.Println("unit 0 depth:", res.Units[0].Directives.Depth)

	// Output:
	// units: 2 err: <nil>
	// unit 0 bump: patch
	// unit 0 depth: all
}
