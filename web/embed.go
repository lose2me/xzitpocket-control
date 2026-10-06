package web

import "embed"

// Files contains the admin UI shipped with the control binary.
//
//go:embed index.html app.js template.js styles.css vendor
var Files embed.FS
