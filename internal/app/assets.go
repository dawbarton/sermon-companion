package app

import "embed"

// StaticFiles contains the browser-dock and review interface.
//
//go:embed static/*
var StaticFiles embed.FS
