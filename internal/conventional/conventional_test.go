package conventional

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/internal/semver"
)

func TestParse(t *testing.T) {
	cases := []struct {
		subject string
		kind    Kind
		scope   string
		desc    string
	}{
		{"fix(core): repair the flux capacitor", KindFix, "core", "repair the flux capacitor"},
		{"feat(app): add dark mode", KindFeat, "app", "add dark mode"},
		{"BREAKING CHANGE(core): drop node 14", KindBreaking, "core", "drop node 14"},
		{"BREAKING-CHANGE(core): drop node 14", KindBreaking, "core", "drop node 14"},
		{"feat(api)!: new response shape", KindBreaking, "api", "new response shape"},
		{"chore(core)!: rewrite build", KindBreaking, "core", "rewrite build"},

		{"chore(core): tidy up", KindOther, "", ""},
		{"docs(readme): typo", KindOther, "", ""},
		{"fix: no scope", KindOther, "", ""},
		{"fix(core) missing colon", KindOther, "", ""},
		{"random message", KindOther, "", ""},
		{"fix(): empty scope", KindOther, "", ""},
		{"fix(two words): bad scope", KindOther, "", ""},
		{"(core): no type", KindOther, "", ""},
		{"", KindOther, "", ""},
		{": leading colon", KindOther, "", ""},
		{"1fix(core)!: numeric type", KindOther, "", ""},
	}
	for _, c := range cases {
		got := Parse(c.subject)
		assert.Equal(t, c.kind, got.Kind, "Parse(%q).Kind", c.subject)
		if got.Kind == KindOther {
			continue
		}
		assert.Equal(t, c.scope, got.Scope, "Parse(%q).Scope", c.subject)
		assert.Equal(t, c.desc, got.Description, "Parse(%q).Description", c.subject)
	}
}

func TestKindBump(t *testing.T) {
	cases := map[Kind]semver.Bump{
		KindOther:    semver.BumpNone,
		KindFix:      semver.BumpPatch,
		KindFeat:     semver.BumpMinor,
		KindBreaking: semver.BumpMajor,
	}
	for kind, want := range cases {
		assert.Equal(t, want, kind.Bump(), "%v.Bump()", kind)
	}
}
