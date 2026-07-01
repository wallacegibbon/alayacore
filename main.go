package main

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/adapters/plainio"
	"github.com/alayacore/alayacore/internal/adapters/rawio"
	"github.com/alayacore/alayacore/internal/adapters/terminal"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/tools"
)

func main() {
	cfg := config.Parse()

	if cfg.ShowVersion {
		fmt.Printf("alayacore version %s\n", config.Version)
		os.Exit(0)
	}

	appCfg, err := app.Setup(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var adapter app.Adapter
	switch {
	case cfg.RawIO:
		adapter = rawio.NewAdapter(appCfg)
	case cfg.PlainIO:
		adapter = plainio.NewAdapter(appCfg)
	default:
		adapter = terminal.NewAdapter(appCfg)
	}

	exitCode := adapter.Start()

	// Clean up this process's temporary files under os.TempDir().
	tools.Cleanup()

	// Clean up MCP server connections (before os.Exit, which skips defers).
	// MCPManager may have been set by the adapter after async init completed.
	if appCfg.MCPManager != nil {
		appCfg.MCPManager.CloseAll()
	} else if appCfg.MCPInit != nil {
		// Init may still be running; close what we have.
		appCfg.MCPInit.Manager().CloseAll()
	}

	os.Exit(exitCode)
}
