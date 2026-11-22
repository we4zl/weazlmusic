package web

import "embed"

//go:embed *.html *.css *.js *.png *.webp
var Files embed.FS
