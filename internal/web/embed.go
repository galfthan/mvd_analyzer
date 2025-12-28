// Package web provides embedded static files for the dashboard.
package web

import "embed"

//go:embed static/*
var StaticFiles embed.FS
