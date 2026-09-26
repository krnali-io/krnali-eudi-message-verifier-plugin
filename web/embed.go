package web

import "embed"

//go:embed *.html *.js *.css *.png
var Files embed.FS
