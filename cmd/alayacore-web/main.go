package main

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/adaptors/websocket"
	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/config"
)

func main() {
	cfg := config.Parse()

	if cfg.ShowVersion {
		fmt.Printf("alayacore-web version %s\n", config.Version)
		os.Exit(0)
	}

	if cfg.ShowHelp {
		printHelp()
		os.Exit(1)
	}

	appCfg, err := app.Setup(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Load model from config file
	modelManager := agentpkg.NewModelManager(cfg.ModelConfig)
	if !modelManager.HasModels() {
		modelPath := modelManager.GetFilePath()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Error: No models configured.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Please edit the model config file:")
		fmt.Fprintf(os.Stderr, "  %s\n", modelPath)
		fmt.Fprintln(os.Stderr, "")
		os.Exit(1)
	}

	// The session will load the model from config when it starts
	// No need to set appCfg.Provider here

	port := cfg.Addr
	if port == "" {
		port = ":8080"
	}

	// Create WebSocket adaptor
	adaptor := websocket.NewAdaptor(port, appCfg)
	adaptor.Start()

	// Wait for interrupt
	select {}
}

func printHelp() {
	fmt.Print(`AlayaCore Web - A WebSocket server for AlayaCore

Usage:
  alayacore-web [flags]

Flags:
  --model-config string   Model config file path (default: ~/.alayacore/model.conf)
  --runtime-config string Runtime config file path (default: ~/.alayacore/runtime.conf)
  --system string         Extra system prompt (can be specified multiple times)
  --skill strings         Skills directory path (can be specified multiple times)
  --addr string           Server address to listen on (default: ":8080")
  --session string        Session file path to load/save conversations
  --proxy string          HTTP proxy URL (e.g., http://127.0.0.1:7890 or socks5://127.0.0.1:1080)
  --max-steps int         Maximum agent loop steps (default: 100)
  --auto-summarize        Automatically summarize conversation when context exceeds 80% of limit
  --debug-api             Write raw API requests and responses to log file
  --version               Show version information
  --help                  Show help information
`)
}
