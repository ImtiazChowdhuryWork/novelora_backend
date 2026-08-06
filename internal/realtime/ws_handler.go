package realtime

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
)

const (
	writeTimeout   = 10 * time.Second
	pongTimeout    = 60 * time.Second
	pingInterval   = 45 * time.Second // must be < pongTimeout
	sendBufferSize = 16
)

var upgrader = websocket.Upgrader{
	// Local development: the dashboard (vite dev server) and the app
	// connect from other origins. Tighten when the platform is hosted.
	CheckOrigin: func(request *http.Request) bool { return true },
}

// client is one connected WebSocket consumer.
type client struct {
	connection *websocket.Conn
	send       chan []byte
	userID     string
}

// NewWSHandler upgrades requests to WebSocket connections registered with
// the hub. Browsers cannot set headers on WebSocket requests, so the
// access token arrives as a query parameter.
//
// The token is optional: every broadcast Event on the hub is a public,
// unfiltered "the catalog changed" signal (see Hub.Run — no per-user
// targeting), and Discover/category screens are fully browsable before
// login. A visitor with no token connects anonymously (userID ""); a
// token that IS present but invalid/expired is still rejected, since
// that's a real auth problem worth surfacing rather than silently
// downgrading to visitor.
func NewWSHandler(hub *Hub, jwtSecret []byte) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		tokenString := request.URL.Query().Get("token")
		var userID string
		if tokenString != "" {
			claims, err := middleware.ValidateAccessToken(jwtSecret, tokenString)
			if err != nil {
				responseWriter.Header().Set("Content-Type", "application/json")
				responseWriter.WriteHeader(http.StatusUnauthorized)
				responseWriter.Write([]byte(`{"error":"access token is invalid or expired"}`))
				return
			}
			userID = claims.UserID
		}

		connection, err := upgrader.Upgrade(responseWriter, request, nil)
		if err != nil {
			log.Printf("realtime: upgrade failed: %v", err)
			return
		}

		connectedClient := &client{
			connection: connection,
			send:       make(chan []byte, sendBufferSize),
			userID:     userID,
		}
		hub.register <- connectedClient

		go connectedClient.writePump()
		go connectedClient.readPump(hub)
	})
}

// writePump delivers hub messages and keeps the connection alive with pings.
func (connectedClient *client) writePump() {
	pingTicker := time.NewTicker(pingInterval)
	defer func() {
		pingTicker.Stop()
		connectedClient.connection.Close()
	}()

	for {
		select {
		case message, open := <-connectedClient.send:
			connectedClient.connection.SetWriteDeadline(time.Now().Add(writeTimeout))
			if !open {
				connectedClient.connection.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := connectedClient.connection.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-pingTicker.C:
			connectedClient.connection.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := connectedClient.connection.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump discards inbound frames (the protocol is server→client only)
// and unregisters on disconnect.
func (connectedClient *client) readPump(hub *Hub) {
	defer func() {
		hub.unregister <- connectedClient
		connectedClient.connection.Close()
	}()

	connectedClient.connection.SetReadLimit(512)
	connectedClient.connection.SetReadDeadline(time.Now().Add(pongTimeout))
	connectedClient.connection.SetPongHandler(func(string) error {
		connectedClient.connection.SetReadDeadline(time.Now().Add(pongTimeout))
		return nil
	})

	for {
		if _, _, err := connectedClient.connection.ReadMessage(); err != nil {
			return
		}
	}
}
