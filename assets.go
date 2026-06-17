package main

import (
	"embed"
	"io/fs"
)

// templatesFS holds the HTML templates, embedded at build time so the binary is
// self-contained and does not depend on its working directory.
//
//go:embed templates
var templatesFS embed.FS

// publicEmbed holds the static assets (CSS, JS, images) under public/.
//
//go:embed public
var publicEmbed embed.FS

// publicFS returns the embedded public/ tree rooted at its contents, so a URL
// path like /public/css/x maps to the FS path css/x.
func publicFS() fs.FS {
	sub, err := fs.Sub(publicEmbed, "public")
	if err != nil {
		// The embedded path is a compile-time constant, so this cannot fail.
		panic(err)
	}
	return sub
}
