package main

import (
	"embed"
	"os"
	"seanime/internal/server"
	"seanime/internal/torrents/trackprobe"
)

//go:embed all:web
var WebFS embed.FS

//go:embed internal/icon/logo.png
var embeddedLogo []byte

func main() {
	// Garceslab Anime: "seanime trackprobe-stats" prints a read-only report of
	// the track-probe cache instead of starting the server.
	if len(os.Args) > 1 && os.Args[1] == trackprobe.StatsCommand {
		os.Exit(trackprobe.RunStatsCommand(os.Args[2:], os.Stdout, os.Stderr))
	}
	server.StartServer(WebFS, embeddedLogo)
}
