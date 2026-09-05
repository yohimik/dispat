package main

import (
	"fmt"
	"net/http"

	"example.invalid/core"
)

func main() {
	http.Handle("/", http.FileServer(http.Dir("/srv/webassets")))
	http.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, core.Version)
	})
	_ = http.ListenAndServe(":8080", nil)
}
