// Package migrations embeds the SQL migrations applied by cmd/migrate (golang-migrate format).
package migrations

import "embed"

// FS holds every NNNN_name.(up|down).sql file of this directory.
//
//go:embed *.sql
var FS embed.FS
