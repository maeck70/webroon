package roon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
)

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
