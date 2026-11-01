//go:build no_web_assets

package main

import "embed"

// A standalone web deployment serves the dashboard assets from its own image,
// so this build keeps the 70 MB asset tree out of the binary. Analytics
// injection needs index.html, which the web image owns.
var buildFS embed.FS
var indexPage []byte
