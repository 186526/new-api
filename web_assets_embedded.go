//go:build !no_web_assets

package main

import "embed"

// The default build serves the dashboard from the same binary. Deployments
// that run the standalone web image build with -tags no_web_assets instead.
//
//go:embed web/dist
var buildFS embed.FS

//go:embed web/dist/index.html
var indexPage []byte
