package scanner_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yohimik/dispat/pkg/scanner"
)

func Example() {
	dir, _ := os.MkdirTemp("", "scanner-example-")
	defer os.RemoveAll(dir)
	manifest := []byte(`{
  "name": "@acme/web",
  "version": "1.2.0",
  "dependencies": {"@acme/core": "workspace:*"},
  "devDependencies": {"typescript": "^5.4.0"}
}`)
	_ = os.WriteFile(filepath.Join(dir, "package.json"), manifest, 0o644)

	mans, err := scanner.New().Scan(context.Background(), dir)
	if err != nil {
		fmt.Println("partial scan:", err)
	}
	for _, m := range mans {
		fmt.Printf("%s %s@%s\n", m.Ecosystem, m.Name, m.Version)
		for _, d := range m.Deps {
			fmt.Printf("  %s %s %q\n", d.Kind, d.Name, d.Range)
		}
	}
	// Output:
	// npm @acme/web@1.2.0
	//   dependencies @acme/core "workspace:*"
	//   devDependencies typescript "^5.4.0"
}
