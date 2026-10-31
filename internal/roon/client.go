package roon

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	svcRegistry  = "com.roonlabs.registry:1"
	svcBrowse    = "com.roonlabs.browse:1"
	svcTransport = "com.roonlabs.transport:2"
	svcImage     = "com.roonlabs.image:1"
	svcPairing   = "com.roonlabs.pairing:1"
	svcPing      = "com.roonlabs.ping:1"

	defaultStateFilePath = "roonstate.json"
)

func getStateFilePath() string {
	if p := os.Getenv("ROON_STATE_FILE"); p != "" {
		return p
	}
	return defaultStateFilePath
}

// EventType identifies live state updates broadcast to connected web clients.
type EventType string

const (
	EventStatusChanged EventType = "status"
	EventZonesChanged  EventType = "zones"
	EventSeekChanged   EventType = "seek"
	EventQueueChanged  EventType = "queue"
)

type Event struct {
	Type   EventType
	ZoneID string
}

// Client manages LAN discovery, Core pairing, subscriptions, and RPC calls to Roon Core.
type Client struct {
	mu            sync.RWMutex
	discoverer    *SoodDiscoverer
	activeConns   map[string]*MooConn // unique_id -> MooConn
	pairedMoo     *MooConn
	coreInfo      *CoreInfo
	status        ConnectionStatus
	zones         map[string]*Zone
	queues        map[string][]QueueItem
	queueUnsubs   map[string]func()
	currentZoneID string
	state         PersistedState

	// Pairing subscription tracking for com.roonlabs.pairing:1
	pairingSubMu   sync.Mutex
	pairingSubReqs map[string]string // mooid -> requestID

	// Event pub/sub for HTMX SSE live updates
	subMu       sync.Mutex
	subscribers map[chan Event]struct{}

	httpClient *http.Client
}

func NewClient() *Client {
	c := &Client{
		activeConns:    make(map[string]*MooConn),
		status:         StatusDiscovering,
		zones:          make(map[string]*Zone),
		queues:         make(map[string][]QueueItem),
		queueUnsubs:    make(map[string]func()),
		pairingSubReqs: make(map[string]string),
		subscribers:    make(map[chan Event]struct{}),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
	c.loadPersistedState()
	c.discoverer = NewSoodDiscoverer(c.onCoreDiscovered)
	return c
}

func (c *Client) Start() {
	log.Println("[Roon] Starting SOOD LAN discovery...")
	c.discoverer.Start()
}

func (c *Client) Stop() {
	c.discoverer.Stop()
	c.mu.Lock()
	for _, conn := range c.activeConns {
		conn.Close()
	}
	c.mu.Unlock()
}

// Status returns the current connection status and CoreInfo if available.
func (c *Client) Status() (ConnectionStatus, *CoreInfo) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.coreInfo == nil {
		return c.status, nil
	}
	cp := *c.coreInfo
	return c.status, &cp
}

// SubscribeEvents registers a channel to receive real-time state updates.
func (c *Client) SubscribeEvents() (chan Event, func()) {
	ch := make(chan Event, 32)
	c.subMu.Lock()
	c.subscribers[ch] = struct{}{}
	c.subMu.Unlock()

	unsub := func() {
		c.subMu.Lock()
		delete(c.subscribers, ch)
		close(ch)
		c.subMu.Unlock()
	}
	return ch, unsub
}

func (c *Client) emit(ev Event) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for ch := range c.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (c *Client) loadPersistedState() {
	c.state.Tokens = make(map[string]string)
	data, err := os.ReadFile(getStateFilePath())
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &c.state)
	if c.state.Tokens == nil {
		c.state.Tokens = make(map[string]string)
	}
	c.currentZoneID = c.state.CurrentZoneID
}

func (c *Client) savePersistedState() {
	data, err := json.MarshalIndent(c.state, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(getStateFilePath(), data, 0644)
}
