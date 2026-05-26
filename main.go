package main

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/adaptors/plainio"
	"github.com/alayacore/alayacore/internal/adaptors/rawio"
	"github.com/alayacore/alayacore/internal/adaptors/terminal"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/config"
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

	var adaptor app.Adaptor
	switch {
	case cfg.RawIO:
		adaptor = rawio.NewAdaptor(appCfg)
	case cfg.PlainIO:
		adaptor = plainio.NewAdaptor(appCfg)
	default:
		adaptor = terminal.NewAdaptor(appCfg)
	}
	os.Exit(adaptor.Start())
}
