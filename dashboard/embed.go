// Package dashboard embeds the built UI (dashboard/public, built from dashboard/ui).
package dashboard

import "embed"

//go:embed all:public
var Public embed.FS
