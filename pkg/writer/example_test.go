package writer_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yohimik/dispat/pkg/writer"
)

func Example() {
	dir, _ := os.MkdirTemp("", "writer-example-")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "package.json")
	_ = os.WriteFile(path, []byte(`{
  "name": "@acme/web",
  "version": "1.2.0",
  "dependencies": {"@acme/core": "^1.2.0"}
}`), 0o644)

	res, err := writer.Rewrite(path, "1.3.0", []writer.Edit{
		{Name: "@acme/core", Kind: "dependencies", Range: "^1.3.0"},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	data, _ := os.ReadFile(path)
	fmt.Printf("applied=%d versionWritten=%v\n%s", len(res.Applied), res.VersionWritten, data)
	// Output:
	// applied=1 versionWritten=true
	// {
	//   "name": "@acme/web",
	//   "version": "1.3.0",
	//   "dependencies": {"@acme/core": "^1.3.0"}
	// }
}
