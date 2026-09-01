package web

import "embed"

// Files contains the admin UI shipped with the control binary.
//
//go:embed index.html app.js styles.css
var Files embed.FS
