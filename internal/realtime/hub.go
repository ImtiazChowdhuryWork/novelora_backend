package realtime

import (
	"encoding/json"
	"log"
)

// Event is what flows to connected clients: a topic naming what changed
// and, when useful, the id of the changed entity. Clients react by
// re-fetching the affected data — events carry no payloads.
type Event struct {
	Topic string `json:"topic"`
	ID    string `json:"id,omitempty"`
}

// Publisher is the write side of the event bus. Services depend on this
// interface only, so the transport can change (in-memory now, Redis
// pub/sub when the backend runs multiple instances) without touching them.
type Publisher interface {
	Publish(event Event)
}

// Hub is the in-memory event bus + WebSocket connection registry.
type Hub struct {
	register   chan *client
	unregister chan *client
	events     chan Event
	clients    map[*client]struct{}
}

func NewHub() *Hub {
	return &Hub{
		register:   make(chan *client),
		unregister: make(chan *client),
		events:     make(chan Event, 64),
		clients:    make(map[*client]struct{}),
	}
}

// Publish broadcasts the event to every connected client. Never blocks
// the caller: slow clients are dropped rather than backpressuring writes.
func (hub *Hub) Publish(event Event) {
	select {
	case hub.events <- event:
	default:
		log.Printf("realtime: event queue full, dropping %s", event.Topic)
	}
}

// Run owns all client-set mutations; start it once in a goroutine.
func (hub *Hub) Run() {
	for {
		select {
		case connectingClient := <-hub.register:
			hub.clients[connectingClient] = struct{}{}
		case disconnectingClient := <-hub.unregister:
			if _, known := hub.clients[disconnectingClient]; known {
				delete(hub.clients, disconnectingClient)
				close(disconnectingClient.send)
			}
		case event := <-hub.events:
			message, err := json.Marshal(event)
			if err != nil {
				log.Printf("realtime: marshal event: %v", err)
				continue
			}
			for connectedClient := range hub.clients {
				select {
				case connectedClient.send <- message:
				default:
					// Slow consumer: drop it; the client reconnects and resyncs
					delete(hub.clients, connectedClient)
					close(connectedClient.send)
				}
			}
		}
	}
}
