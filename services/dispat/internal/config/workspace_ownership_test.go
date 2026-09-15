package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

func TestValidatePackageOwnershipPathBoundaries(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name    string
		paths   []string
		overlap bool
	}{
		{"siblings", []string{"a", "a-b", "ab", "b"}, false},
		{"descendant past lexical sibling", []string{"a", "a-b", "a/child"}, true},
		{"equal cleaned paths", []string{"a/b/..", "a"}, true},
		{"root ancestor", []string{".", "a/child"}, true},
		{"disjoint children", []string{"a/b", "a/c"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkgs := make([]*model.Package, len(tc.paths))
			for i, path := range tc.paths {
				dir := filepath.Join(root, filepath.FromSlash(path))
				require.NoError(t, os.MkdirAll(dir, 0o755))
				pkgs[i] = &model.Package{Name: fmt.Sprint(i), Dir: dir}
			}
			before := append([]*model.Package(nil), pkgs...)
			err := validatePackageOwnership(pkgs)
			if tc.overlap {
				assert.ErrorContains(t, err, "ownership overlaps")
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, before, pkgs, "validation preserves discovery order")
		})
	}
}

func TestValidatePackageOwnershipRejectsCanonicalPathAliases(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	require.NoError(t, os.Mkdir(actual, 0o755))
	alias := filepath.Join(root, "alias")
	require.NoError(t, os.Symlink(actual, alias))

	err := validatePackageOwnership([]*model.Package{
		{Name: "first", Dir: actual},
		{Name: "second", Dir: alias},
	})
	require.ErrorContains(t, err, "ownership overlaps")
}

func BenchmarkValidatePackageOwnership(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := b.TempDir()
			pkgs := make([]*model.Package, count)
			for i := range pkgs {
				name := fmt.Sprintf("pkg%d", i)
				dir := filepath.Join(root, name)
				if err := os.Mkdir(dir, 0o755); err != nil {
					b.Fatal(err)
				}
				pkgs[i] = &model.Package{Name: name, Dir: dir}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := validatePackageOwnership(pkgs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
