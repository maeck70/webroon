package roon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
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

func (c *Client) onCoreDiscovered(msg SoodMessage) {
	uniqueID := msg.Props["unique_id"]
	host := msg.FromIP
	port := msg.Props["http_port"]

	c.mu.Lock()
	if _, exists := c.activeConns[uniqueID]; exists {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	log.Printf("[Roon] Connecting to discovered Roon Core %s at %s:%s...", uniqueID, host, port)
	conn, err := DialMoo(host, port, uniqueID, c.handleCoreRequest, func() {
		c.onConnClosed(uniqueID)
	})
	if err != nil {
		log.Printf("[Roon] Failed to dial Core %s:%s: %v", host, port, err)
		return
	}

	c.mu.Lock()
	if _, exists := c.activeConns[uniqueID]; exists {
		c.mu.Unlock()
		conn.Close()
		return
	}
	c.activeConns[uniqueID] = conn
	c.mu.Unlock()

	go c.handshake(conn)
}

func (c *Client) onConnClosed(uniqueID string) {
	log.Printf("[Roon] Connection closed for Core %s", uniqueID)
	c.mu.Lock()
	delete(c.activeConns, uniqueID)
	wasPaired := c.pairedMoo != nil && c.pairedMoo.UniqueID == uniqueID
	if wasPaired {
		c.pairedMoo = nil
		c.coreInfo = nil
		c.status = StatusDiscovering
		c.zones = make(map[string]*Zone)
		c.queues = make(map[string][]QueueItem)
		c.queueUnsubs = make(map[string]func())
	}
	c.mu.Unlock()

	if wasPaired {
		c.emit(Event{Type: EventStatusChanged})
		c.discoverer.SendQuery()
	}
}

func (c *Client) handshake(conn *MooConn) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var info CoreInfo
	msg, err := conn.Call(ctx, svcRegistry+"/info", nil, &info)
	if err != nil || msg == nil {
		log.Printf("[Roon] Failed registry/info on %s:%s: %v", conn.Host, conn.Port, err)
		conn.Close()
		return
	}
	info.Host = conn.Host
	info.Port = conn.Port

	c.mu.Lock()
	token := c.state.Tokens[info.CoreID]
	if c.status == StatusDiscovering {
		c.status = StatusWaitingPermission
		c.coreInfo = &info
	}
	c.mu.Unlock()
	c.emit(Event{Type: EventStatusChanged})

	regReq := map[string]any{
		"extension_id":    "com.webroon.htmx.controller",
		"display_name":    "WebRoon Web Controller",
		"display_version": "1.0.0",
		"publisher":       "WebRoon",
		"email":           "controller@webroon.local",
		"required_services": []string{
			svcBrowse,
			svcTransport,
			svcImage,
		},
		"optional_services": []string{},
		"provided_services": []string{
			svcPairing,
			svcPing,
		},
	}
	if token != "" {
		regReq["token"] = token
	}

	_, err = conn.SendRequest(svcRegistry+"/register", regReq, func(regMsg *MooMessage, body []byte) {
		if regMsg == nil {
			return
		}
		if regMsg.Name == "Registered" {
			var regInfo CoreInfo
			if err := json.Unmarshal(body, &regInfo); err != nil {
				log.Printf("[Roon] Failed to parse Registered body: %v", err)
				return
			}
			regInfo.Host = conn.Host
			regInfo.Port = conn.Port

			log.Printf("[Roon] Paired and registered with Roon Core: %s (%s)", regInfo.DisplayName, regInfo.CoreID)

			c.mu.Lock()
			c.state.Tokens[regInfo.CoreID] = regInfo.Token
			c.state.PairedCoreID = regInfo.CoreID
			c.savePersistedState()

			c.pairedMoo = conn
			c.coreInfo = &regInfo
			c.status = StatusConnected
			c.mu.Unlock()

			c.notifyPairingSubscribers(conn, regInfo.CoreID)
			c.emit(Event{Type: EventStatusChanged})
			c.subscribeZones(conn)
		}
	})
	if err != nil {
		log.Printf("[Roon] Failed to send registry/register: %v", err)
		conn.Close()
	}
}

func (c *Client) handleCoreRequest(conn *MooConn, msg *MooMessage) {
	switch msg.Service {
	case svcPing:
		if msg.Name == "ping" {
			_ = conn.SendComplete(msg.RequestID, "Success", nil)
			return
		}
	case svcPairing:
		c.mu.RLock()
		pairedID := c.state.PairedCoreID
		if c.coreInfo != nil && pairedID == "" {
			pairedID = c.coreInfo.CoreID
		}
		c.mu.RUnlock()

		switch msg.Name {
		case "subscribe_pairing":
			c.pairingSubMu.Lock()
			c.pairingSubReqs[conn.UniqueID] = msg.RequestID
			c.pairingSubMu.Unlock()
			_ = conn.SendContinue(msg.RequestID, "Subscribed", map[string]any{
				"paired_core_id": pairedID,
			})
			return
		case "unsubscribe_pairing":
			c.pairingSubMu.Lock()
			delete(c.pairingSubReqs, conn.UniqueID)
			c.pairingSubMu.Unlock()
			_ = conn.SendComplete(msg.RequestID, "Unsubscribed", nil)
			return
		case "get_pairing":
			_ = conn.SendComplete(msg.RequestID, "Success", map[string]any{
				"paired_core_id": pairedID,
			})
			return
		case "pair":
			c.mu.Lock()
			if c.coreInfo != nil {
				c.state.PairedCoreID = c.coreInfo.CoreID
				pairedID = c.coreInfo.CoreID
				c.savePersistedState()
			}
			c.mu.Unlock()
			c.notifyPairingSubscribers(conn, pairedID)
			_ = conn.SendComplete(msg.RequestID, "Success", nil)
			return
		}
	}

	_ = conn.SendComplete(msg.RequestID, "InvalidRequest", map[string]string{
		"error": fmt.Sprintf("unknown request: %s/%s", msg.Service, msg.Name),
	})
}

func (c *Client) notifyPairingSubscribers(conn *MooConn, pairedCoreID string) {
	c.pairingSubMu.Lock()
	reqID, ok := c.pairingSubReqs[conn.UniqueID]
	c.pairingSubMu.Unlock()
	if ok && reqID != "" {
		_ = conn.SendContinue(reqID, "Changed", map[string]any{
			"paired_core_id": pairedCoreID,
		})
	}
}

type zonesSubPayload struct {
	Zones            []Zone     `json:"zones"`
	ZonesAdded       []Zone     `json:"zones_added"`
	ZonesChanged     []Zone     `json:"zones_changed"`
	ZonesRemoved     []string   `json:"zones_removed"`
	ZonesSeekChanged []seekInfo `json:"zones_seek_changed"`
}

type seekInfo struct {
	ZoneID             string `json:"zone_id"`
	SeekPosition       *int   `json:"seek_position"`
	QueueTimeRemaining int    `json:"queue_time_remaining"`
}

func (c *Client) subscribeZones(conn *MooConn) {
	_, err := conn.Subscribe(svcTransport, "zones", map[string]any{}, func(msg *MooMessage, body []byte) {
		if msg == nil || len(body) == 0 {
			return
		}
		var payload zonesSubPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			log.Printf("[Roon] Failed to parse zones subscription payload: %v", err)
			return
		}

		c.mu.Lock()
		fullChange := false
		seekOnly := false

		switch msg.Name {
		case "Subscribed":
			fullChange = true
			c.zones = make(map[string]*Zone)
			for i := range payload.Zones {
				z := payload.Zones[i]
				c.zones[z.ZoneID] = &z
			}
		case "Changed":
			if len(payload.ZonesRemoved) > 0 {
				fullChange = true
				for _, zid := range payload.ZonesRemoved {
					delete(c.zones, zid)
					if unsub, ok := c.queueUnsubs[zid]; ok {
						unsub()
						delete(c.queueUnsubs, zid)
					}
					delete(c.queues, zid)
				}
			}
			if len(payload.ZonesAdded) > 0 {
				fullChange = true
				for i := range payload.ZonesAdded {
					z := payload.ZonesAdded[i]
					c.zones[z.ZoneID] = &z
				}
			}
			if len(payload.ZonesChanged) > 0 {
				fullChange = true
				for i := range payload.ZonesChanged {
					z := payload.ZonesChanged[i]
					c.zones[z.ZoneID] = &z
				}
			}
			if len(payload.ZonesSeekChanged) > 0 {
				seekOnly = !fullChange
				for _, s := range payload.ZonesSeekChanged {
					if z, ok := c.zones[s.ZoneID]; ok {
						z.SeekPosition = s.SeekPosition
						z.QueueTimeRemaining = s.QueueTimeRemaining
						if z.NowPlaying != nil {
							z.NowPlaying.SeekPosition = s.SeekPosition
						}
					}
				}
			}
		}

		// Ensure we have a valid currentZoneID selected
		if c.currentZoneID == "" || c.zones[c.currentZoneID] == nil {
			c.currentZoneID = c.pickDefaultZoneIDLocked()
			if c.currentZoneID != "" {
				c.state.CurrentZoneID = c.currentZoneID
				c.savePersistedState()
			}
		}
		activeZoneID := c.currentZoneID
		needQueueSub := activeZoneID != "" && c.queueUnsubs[activeZoneID] == nil
		c.mu.Unlock()

		if needQueueSub {
			c.ensureQueueSubscription(conn, activeZoneID)
		}

		if fullChange {
			c.emit(Event{Type: EventZonesChanged, ZoneID: activeZoneID})
		} else if seekOnly {
			c.emit(Event{Type: EventSeekChanged, ZoneID: activeZoneID})
		}
	})
	if err != nil {
		log.Printf("[Roon] Failed to subscribe to zones: %v", err)
	}
}

func (c *Client) pickDefaultZoneIDLocked() string {
	if len(c.zones) == 0 {
		return ""
	}
	ids := make([]string, 0, len(c.zones))
	for id, z := range c.zones {
		if z.State == "playing" {
			return id
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids[0]
}

type queueSubPayload struct {
	Items   []QueueItem   `json:"items"`
	Changes []queueChange `json:"changes"`
}

type queueChange struct {
	Operation string      `json:"operation"` // insert, remove
	Index     int         `json:"index"`
	Items     []QueueItem `json:"items,omitempty"`
	Count     int         `json:"count,omitempty"`
}

func (c *Client) ensureQueueSubscription(conn *MooConn, zoneID string) {
	if conn == nil || zoneID == "" {
		return
	}
	c.mu.Lock()
	if _, exists := c.queueUnsubs[zoneID]; exists {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	unsub, err := conn.Subscribe(svcTransport, "queue", map[string]any{
		"zone_or_output_id": zoneID,
		"max_item_count":    100,
	}, func(msg *MooMessage, body []byte) {
		if msg == nil || len(body) == 0 {
			return
		}
		var payload queueSubPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			return
		}

		c.mu.Lock()
		switch msg.Name {
		case "Subscribed":
			c.queues[zoneID] = payload.Items
		case "Changed":
			items := append([]QueueItem(nil), c.queues[zoneID]...)
			for _, chg := range payload.Changes {
				switch chg.Operation {
				case "insert":
					idx := chg.Index
					if idx < 0 {
						idx = 0
					}
					if idx > len(items) {
						idx = len(items)
					}
					next := make([]QueueItem, 0, len(items)+len(chg.Items))
					next = append(next, items[:idx]...)
					next = append(next, chg.Items...)
					next = append(next, items[idx:]...)
					items = next
				case "remove":
					idx := chg.Index
					end := idx + chg.Count
					if idx >= 0 && idx < len(items) {
						if end > len(items) {
							end = len(items)
						}
						items = append(items[:idx], items[end:]...)
					}
				}
			}
			c.queues[zoneID] = items
		}
		c.mu.Unlock()
		c.emit(Event{Type: EventQueueChanged, ZoneID: zoneID})
	})
	if err == nil {
		c.mu.Lock()
		c.queueUnsubs[zoneID] = unsub
		c.mu.Unlock()
	}
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

// GetZones returns a sorted slice of all active Zones and the currently selected ZoneID.
func (c *Client) GetZones() ([]Zone, *Zone, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	list := make([]Zone, 0, len(c.zones))
	for _, z := range c.zones {
		list = append(list, *z)
	}
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i].DisplayName) < strings.ToLower(list[j].DisplayName)
	})

	var current *Zone
	if z, ok := c.zones[c.currentZoneID]; ok {
		cp := *z
		current = &cp
	} else if len(list) > 0 {
		cp := list[0]
		current = &cp
	}
	curID := c.currentZoneID
	if current != nil {
		curID = current.ZoneID
	}
	return list, current, curID
}

// SetCurrentZone switches the active zone controlled by the UI.
func (c *Client) SetCurrentZone(zoneID string) {
	c.mu.Lock()
	if _, ok := c.zones[zoneID]; ok {
		c.currentZoneID = zoneID
		c.state.CurrentZoneID = zoneID
		c.savePersistedState()
	}
	conn := c.pairedMoo
	c.mu.Unlock()

	if conn != nil && zoneID != "" {
		c.ensureQueueSubscription(conn, zoneID)
	}
	c.emit(Event{Type: EventZonesChanged, ZoneID: zoneID})
	c.emit(Event{Type: EventQueueChanged, ZoneID: zoneID})
}

// GetQueue returns the current queue items for the active zone.
func (c *Client) GetQueue(zoneID string) []QueueItem {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if zoneID == "" {
		zoneID = c.currentZoneID
	}
	items := c.queues[zoneID]
	out := make([]QueueItem, len(items))
	copy(out, items)
	return out
}

// Browse calls com.roonlabs.browse:1/browse.
func (c *Client) Browse(ctx context.Context, opts BrowseOpts) (*BrowseResult, error) {
	c.mu.RLock()
	conn := c.pairedMoo
	if opts.ZoneOrOutputID == "" {
		opts.ZoneOrOutputID = c.currentZoneID
	}
	c.mu.RUnlock()

	if conn == nil {
		return nil, errors.New("not connected to Roon Core")
	}
	if opts.Hierarchy == "" {
		opts.Hierarchy = "browse"
	}

	var res BrowseResult
	msg, err := conn.Call(ctx, svcBrowse+"/browse", opts, &res)
	if err != nil {
		return nil, err
	}
	if msg.Name != "Success" {
		return nil, fmt.Errorf("browse error: %s", msg.Name)
	}
	return &res, nil
}

// Load calls com.roonlabs.browse:1/load.
func (c *Client) Load(ctx context.Context, opts LoadOpts) (*LoadResult, error) {
	c.mu.RLock()
	conn := c.pairedMoo
	c.mu.RUnlock()

	if conn == nil {
		return nil, errors.New("not connected to Roon Core")
	}
	if opts.Hierarchy == "" {
		opts.Hierarchy = "browse"
	}
	if opts.Count <= 0 {
		opts.Count = 100
	}

	var res LoadResult
	msg, err := conn.Call(ctx, svcBrowse+"/load", opts, &res)
	if err != nil {
		return nil, err
	}
	if msg.Name != "Success" {
		return nil, fmt.Errorf("load error: %s", msg.Name)
	}
	return &res, nil
}

// GlobalSearch executes a search across both the local library and connected streaming services (Tidal/Qobuz).
func (c *Client) GlobalSearch(ctx context.Context, query string) (*LoadResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search query is empty")
	}

	c.mu.RLock()
	zoneID := c.currentZoneID
	c.mu.RUnlock()

	// Step 1: Reset the "browse" hierarchy to root and check for a Search input_prompt item at root or under Library
	_, err := c.Browse(ctx, BrowseOpts{
		Hierarchy:      "browse",
		PopAll:         true,
		ZoneOrOutputID: zoneID,
	})
	if err != nil {
		return nil, err
	}

	rootLoad, err := c.Load(ctx, LoadOpts{
		Hierarchy: "browse",
		Offset:    0,
		Count:     100,
	})
	if err != nil {
		return nil, err
	}

	var searchItemKey string
	var libraryItemKey string
	for _, it := range rootLoad.Items {
		if it.InputPrompt != nil {
			searchItemKey = it.ItemKey
			break
		}
		if strings.EqualFold(it.Title, "Library") || strings.EqualFold(it.Title, "Search") {
			if strings.EqualFold(it.Title, "Search") && it.ItemKey != "" {
				searchItemKey = it.ItemKey
				break
			}
			libraryItemKey = it.ItemKey
		}
	}

	// If Search prompt is inside "Library", drill into Library to get its item_key
	if searchItemKey == "" && libraryItemKey != "" {
		_, err = c.Browse(ctx, BrowseOpts{
			Hierarchy:      "browse",
			ItemKey:        libraryItemKey,
			ZoneOrOutputID: zoneID,
		})
		if err == nil {
			libLoad, err := c.Load(ctx, LoadOpts{
				Hierarchy: "browse",
				Offset:    0,
				Count:     100,
			})
			if err == nil {
				for _, it := range libLoad.Items {
					if it.InputPrompt != nil || strings.EqualFold(it.Title, "Search") {
						searchItemKey = it.ItemKey
						break
					}
				}
			}
		}
	}

	if searchItemKey != "" {
		_, err = c.Browse(ctx, BrowseOpts{
			Hierarchy:      "browse",
			ItemKey:        searchItemKey,
			Input:          query,
			ZoneOrOutputID: zoneID,
		})
		if err != nil {
			return nil, err
		}
		return c.Load(ctx, LoadOpts{
			Hierarchy: "browse",
			Offset:    0,
			Count:     100,
		})
	}

	// Fallback: use dedicated "search" hierarchy directly
	_, err = c.Browse(ctx, BrowseOpts{
		Hierarchy:      "search",
		PopAll:         true,
		ZoneOrOutputID: zoneID,
	})
	if err == nil {
		sLoad, lerr := c.Load(ctx, LoadOpts{
			Hierarchy: "search",
			Offset:    0,
			Count:     100,
		})
		if lerr == nil {
			for _, it := range sLoad.Items {
				if it.InputPrompt != nil && it.ItemKey != "" {
					_, _ = c.Browse(ctx, BrowseOpts{
						Hierarchy:      "search",
						ItemKey:        it.ItemKey,
						Input:          query,
						ZoneOrOutputID: zoneID,
					})
					return c.Load(ctx, LoadOpts{
						Hierarchy: "search",
						Offset:    0,
						Count:     100,
					})
				}
			}
		}
	}

	return nil, errors.New("search prompt not found in Roon browse hierarchy")
}

// Control executes transport playback commands: play, pause, playpause, stop, previous, next.
func (c *Client) Control(ctx context.Context, zoneID, control string) error {
	c.mu.RLock()
	conn := c.pairedMoo
	if zoneID == "" {
		zoneID = c.currentZoneID
	}
	c.mu.RUnlock()

	if conn == nil || zoneID == "" {
		return errors.New("no active zone")
	}

	msg, err := conn.Call(ctx, svcTransport+"/control", map[string]any{
		"zone_or_output_id": zoneID,
		"control":           control,
	}, nil)
	if err != nil {
		return err
	}
	if msg.Name != "Success" {
		return fmt.Errorf("transport control failed: %s", msg.Name)
	}
	return nil
}

// Seek seeks to a relative or absolute position in seconds.
func (c *Client) Seek(ctx context.Context, zoneID, how string, seconds int) error {
	c.mu.RLock()
	conn := c.pairedMoo
	if zoneID == "" {
		zoneID = c.currentZoneID
	}
	c.mu.RUnlock()

	if conn == nil || zoneID == "" {
		return errors.New("no active zone")
	}

	msg, err := conn.Call(ctx, svcTransport+"/seek", map[string]any{
		"zone_or_output_id": zoneID,
		"how":               how,
		"seconds":           seconds,
	}, nil)
	if err != nil {
		return err
	}
	if msg.Name != "Success" {
		return fmt.Errorf("seek failed: %s", msg.Name)
	}
	return nil
}

// ChangeVolume adjusts the volume of an output (how: "absolute", "relative", "relative_step").
func (c *Client) ChangeVolume(ctx context.Context, outputID, how string, value float64) error {
	c.mu.RLock()
	conn := c.pairedMoo
	c.mu.RUnlock()

	if conn == nil || outputID == "" {
		return errors.New("no output specified")
	}

	msg, err := conn.Call(ctx, svcTransport+"/change_volume", map[string]any{
		"output_id": outputID,
		"how":       how,
		"value":     value,
	}, nil)
	if err != nil {
		return err
	}
	if msg.Name != "Success" {
		return fmt.Errorf("change_volume failed: %s", msg.Name)
	}
	return nil
}

// Mute mutes or unmutes an output (how: "mute", "unmute").
func (c *Client) Mute(ctx context.Context, outputID, how string) error {
	c.mu.RLock()
	conn := c.pairedMoo
	c.mu.RUnlock()

	if conn == nil || outputID == "" {
		return errors.New("no output specified")
	}

	msg, err := conn.Call(ctx, svcTransport+"/mute", map[string]any{
		"output_id": outputID,
		"how":       how,
	}, nil)
	if err != nil {
		return err
	}
	if msg.Name != "Success" {
		return fmt.Errorf("mute failed: %s", msg.Name)
	}
	return nil
}

// ChangeSettings updates zone settings (shuffle, loop, auto_radio).
func (c *Client) ChangeSettings(ctx context.Context, zoneID string, settings map[string]any) error {
	c.mu.RLock()
	conn := c.pairedMoo
	if zoneID == "" {
		zoneID = c.currentZoneID
	}
	c.mu.RUnlock()

	if conn == nil || zoneID == "" {
		return errors.New("no active zone")
	}

	req := make(map[string]any, len(settings)+1)
	for k, v := range settings {
		req[k] = v
	}
	req["zone_or_output_id"] = zoneID

	msg, err := conn.Call(ctx, svcTransport+"/change_settings", req, nil)
	if err != nil {
		return err
	}
	if msg.Name != "Success" {
		return fmt.Errorf("change_settings failed: %s", msg.Name)
	}
	return nil
}

// PlayFromHere starts playback from a specific item in the zone's queue.
func (c *Client) PlayFromHere(ctx context.Context, zoneID string, queueItemID int) error {
	c.mu.RLock()
	conn := c.pairedMoo
	if zoneID == "" {
		zoneID = c.currentZoneID
	}
	c.mu.RUnlock()

	if conn == nil || zoneID == "" {
		return errors.New("no active zone")
	}

	msg, err := conn.Call(ctx, svcTransport+"/play_from_here", map[string]any{
		"zone_or_output_id": zoneID,
		"queue_item_id":     queueItemID,
	}, nil)
	if err != nil {
		return err
	}
	if msg.Name != "Success" {
		return fmt.Errorf("play_from_here failed: %s", msg.Name)
	}
	return nil
}

// FetchImage fetches artwork from the Roon Core HTTP image endpoint or MOO com.roonlabs.image:1/get_image.
func (c *Client) FetchImage(ctx context.Context, imageKey, scale string, width, height int) ([]byte, string, error) {
	c.mu.RLock()
	core := c.coreInfo
	conn := c.pairedMoo
	c.mu.RUnlock()

	if core == nil || imageKey == "" {
		return nil, "", errors.New("not connected or empty image_key")
	}

	if scale == "" {
		scale = "fit"
	}
	if width <= 0 {
		width = 300
	}
	if height <= 0 {
		height = 300
	}

	// Fast path: HTTP image endpoint on Roon Core
	q := url.Values{}
	q.Set("scale", scale)
	q.Set("width", fmt.Sprintf("%d", width))
	q.Set("height", fmt.Sprintf("%d", height))
	q.Set("format", "image/jpeg")

	imgURL := fmt.Sprintf("http://%s:%s/api/image/%s?%s", core.Host, core.Port, url.PathEscape(imageKey), q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err == nil {
		resp, err := c.httpClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				data, err := io.ReadAll(resp.Body)
				if err == nil && len(data) > 0 {
					ct := resp.Header.Get("Content-Type")
					if ct == "" {
						ct = "image/jpeg"
					}
					return data, ct, nil
				}
			}
		}
	}

	// Fallback: MOO com.roonlabs.image:1/get_image
	if conn != nil {
		msg, err := conn.Call(ctx, svcImage+"/get_image", map[string]any{
			"image_key": imageKey,
			"scale":     scale,
			"width":     width,
			"height":    height,
			"format":    "image/jpeg",
		}, nil)
		if err == nil && msg != nil && msg.Name == "Success" && len(msg.Body) > 0 {
			ct := msg.ContentType
			if ct == "" {
				ct = "image/jpeg"
			}
			return msg.Body, ct, nil
		}
	}

	return nil, "", errors.New("image not found")
}
