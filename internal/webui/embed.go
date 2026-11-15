package webui

import "embed"

// Assets contains the single-page admin console. It is embedded so the app
// stays a single binary inside the fnOS package.
//
//go:embed assets/*
var Assets embed.FS
