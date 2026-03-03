package adaptors

import (
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	_ "embed"

	agentpkg "github.com/wallacegibbon/coreclaw/internal/agent"
	"github.com/wallacegibbon/coreclaw/internal/app"
	"github.com/wallacegibbon/coreclaw/internal/stream"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// WebSocketAdaptor connects WebSocket to the agent processor
type WebSocketAdaptor struct {
	Config *app.Config
	Server *http.Server
}

// NewWebSocketAdaptor creates a new WebSocket adaptor that listens on the given port
// Each client gets its own agent session
func NewWebSocketAdaptor(port string, cfg *app.Config) *WebSocketAdaptor {
	mux := http.NewServeMux()

	// Handle WebSocket
	mux.HandleFunc("/ws", handleWebSocket(cfg))

	// Serve embedded index.html
	mux.HandleFunc("/", serveIndex)

	server := &http.Server{
		Addr:    port,
		Handler: mux,
	}

	return &WebSocketAdaptor{
		Config: cfg,
		Server: server,
	}
}

// serveIndex serves the embedded index.html
func serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := strings.Replace(string(indexHTML), "{{welcome}}", WelcomeText, 1)
	w.Write([]byte(html))
}

// Start starts the WebSocket server in a goroutine
func (a *WebSocketAdaptor) Start() {
	go func() {
		a.Server.ListenAndServe()
	}()
}

// handleWebSocket handles WebSocket connections with per-client sessions
func handleWebSocket(cfg *app.Config) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Create per-client streams
		input := stream.NewChanInput(100)
		output := &clientOutput{
			conn:    conn,
			closeCh: make(chan struct{}),
		}
		defer close(output.closeCh)

		// Load or create session
		session, _ := agentpkg.LoadOrNewSession(
			cfg.Model,
			cfg.AgentTools,
			cfg.SystemPrompt,
			cfg.Cfg.BaseURL,
			cfg.Cfg.ModelName,
			input,
			output,
			cfg.Cfg.Session,
		)
		output.session = session

		// Display loaded messages if session has any
		if len(session.Messages) > 0 {
			session.DisplayMessages()
			// Force flush to ensure all messages are written to display buffer
			session.Output.Flush()
		}

		// Read loop - handles client input and blocks until connection closes
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}

			if len(message) == 0 {
				continue
			}

			// Decode TLV to check for /quit command
			if len(message) >= 5 {
				tag := message[0]
				length := uint32(message[1])<<24 | uint32(message[2])<<16 | uint32(message[3])<<8 | uint32(message[4])
				if len(message) >= 5+int(length) {
					value := string(message[5 : 5+length])
					// Ignore /quit and /exit commands from web client
					if tag == 'A' && (value == "/quit" || value == "/exit") {
						continue
					}
				}
			}

			// Client already encoded as TLV, pass through raw data
			input.EmitRawData(message)
		}
	}
}

// clientOutput implements stream.Output for a single WebSocket client
type clientOutput struct {
	conn    *websocket.Conn
	session *agentpkg.Session
	mu      sync.Mutex
	closeCh chan struct{}
}

// Write implements stream.Output
func (o *clientOutput) Write(p []byte) (n int, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	err = o.conn.WriteMessage(websocket.BinaryMessage, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// WriteString implements stream.Output
func (o *clientOutput) WriteString(s string) (int, error) {
	return o.Write([]byte(s))
}

// Flush implements stream.Output
func (o *clientOutput) Flush() error {
	return nil
}

//go:embed chat.html
var indexHTML []byte
