// Package web embeds the panel frontend into the Go binary.
package web

import "embed"

// Static contains the dependency-free frontend.
//
//go:embed static/*
var Static embed.FS
