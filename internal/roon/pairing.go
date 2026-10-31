package roon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

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
