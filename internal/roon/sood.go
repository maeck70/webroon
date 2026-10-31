package roon

import (
	"bytes"
	"encoding/binary"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	soodPort        = 9003
	soodMulticastIP = "239.255.90.90"
	RoonServiceID   = "00720724-5143-4a9b-abac-0e50cba674bb"
)

// SoodMessage represents a parsed SOOD discovery packet.
type SoodMessage struct {
	FromIP   string
	FromPort string
	Type     string
	Props    map[string]string
}

// SoodDiscoverer handles UDP multicast/broadcast discovery for Roon Cores on the LAN.
type SoodDiscoverer struct {
	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	conns   []*net.UDPConn
	onCore  func(msg SoodMessage)
}

func NewSoodDiscoverer(onCore func(msg SoodMessage)) *SoodDiscoverer {
	return &SoodDiscoverer{
		onCore: onCore,
	}
}

func (s *SoodDiscoverer) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	go func() {
		s.initSockets()
		s.SendQuery()

		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.SendQuery()
			}
		}
	}()
}

func (s *SoodDiscoverer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stopCh)
	for _, c := range s.conns {
		_ = c.Close()
	}
	s.conns = nil
}

func isVirtualInterface(name string) bool {
	n := strings.ToLower(name)
	prefixes := []string{"cali", "veth", "docker", "flannel", "cni", "br-", "virbr", "tunl", "vxlan", "kube", "lo"}
	for _, p := range prefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

func (s *SoodDiscoverer) initSockets() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Bind a general ephemeral UDP4 socket for sending and receiving unicast replies
	if conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0}); err == nil {
		s.conns = append(s.conns, conn)
		go s.readLoop(conn)
	}

	// 2. Bind per-LAN-interface sockets and multicast listener on port 9003
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}

	mcastAddr := &net.UDPAddr{IP: net.ParseIP(soodMulticastIP), Port: soodPort}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || isVirtualInterface(iface.Name) {
			continue
		}
		if iface.Flags&net.FlagMulticast != 0 {
			if mconn, err := net.ListenMulticastUDP("udp4", &iface, mcastAddr); err == nil {
				s.conns = append(s.conns, mconn)
				go s.readLoop(mconn)
			}
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			if uconn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip4, Port: 0}); err == nil {
				s.conns = append(s.conns, uconn)
				go s.readLoop(uconn)
			}
		}
	}
}

func (s *SoodDiscoverer) SendQuery() {
	pkt := encodeSoodQuery(map[string]string{
		"query_service_id": RoonServiceID,
		"_tid":             uuid.NewString(),
	})

	mcastAddr := &net.UDPAddr{IP: net.ParseIP(soodMulticastIP), Port: soodPort}
	bcastGlobal := &net.UDPAddr{IP: net.IPv4bcast, Port: soodPort}
	loopbackAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: soodPort}

	// Collect physical LAN subnet broadcast addresses
	var bcastAddrs []*net.UDPAddr
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || isVirtualInterface(iface.Name) {
				continue
			}
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				if ipNet, ok := addr.(*net.IPNet); ok {
					if ip4 := ipNet.IP.To4(); ip4 != nil && !ip4.IsLinkLocalUnicast() && len(ipNet.Mask) == 4 {
						bcast := make(net.IP, 4)
						for i := 0; i < 4; i++ {
							bcast[i] = ip4[i] | (^ipNet.Mask[i])
						}
						bcastAddrs = append(bcastAddrs, &net.UDPAddr{IP: bcast, Port: soodPort})
					}
				}
			}
		}
	}

	s.mu.Lock()
	conns := append([]*net.UDPConn(nil), s.conns...)
	s.mu.Unlock()

	for _, c := range conns {
		_, _ = c.WriteToUDP(pkt, mcastAddr)
		_, _ = c.WriteToUDP(pkt, bcastGlobal)
		_, _ = c.WriteToUDP(pkt, loopbackAddr)
		for _, baddr := range bcastAddrs {
			_, _ = c.WriteToUDP(pkt, baddr)
		}
	}
}

func (s *SoodDiscoverer) readLoop(conn *net.UDPConn) {
	buf := make([]byte, 65535)
	for {
		n, raddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		msg := parseSoodPacket(buf[:n], raddr)
		if msg == nil || msg.Type == "Q" {
			continue
		}
		if msg.Props["service_id"] == RoonServiceID && msg.Props["unique_id"] != "" && msg.Props["http_port"] != "" {
			if isLocalAddress(msg.FromIP) {
				msg.FromIP = "127.0.0.1"
			}
			if s.onCore != nil {
				s.onCore(*msg)
			}
		}
	}
}

func encodeSoodQuery(props map[string]string) []byte {
	var buf bytes.Buffer
	buf.WriteString("SOOD")
	buf.WriteByte(2)
	buf.WriteByte('Q')

	for k, v := range props {
		kBytes := []byte(k)
		buf.WriteByte(byte(len(kBytes)))
		buf.Write(kBytes)

		vBytes := []byte(v)
		var vlen [2]byte
		binary.BigEndian.PutUint16(vlen[:], uint16(len(vBytes)))
		buf.Write(vlen[:])
		buf.Write(vBytes)
	}
	return buf.Bytes()
}

func parseSoodPacket(data []byte, raddr *net.UDPAddr) *SoodMessage {
	if len(data) < 6 {
		return nil
	}
	if string(data[0:4]) != "SOOD" || data[4] != 2 {
		return nil
	}
	msg := &SoodMessage{
		FromIP: raddr.IP.String(),
		Type:   string(data[5:6]),
		Props:  make(map[string]string),
	}

	pos := 6
	for pos < len(data) {
		klen := int(data[pos])
		pos++
		if klen == 0 || pos+klen > len(data) {
			return nil
		}
		name := string(data[pos : pos+klen])
		pos += klen

		if pos+2 > len(data) {
			return nil
		}
		vlen := (int(data[pos]) << 8) | int(data[pos+1])
		pos += 2

		var val string
		if vlen == 0xffff {
			val = ""
		} else if vlen == 0 {
			val = ""
		} else {
			if pos+vlen > len(data) {
				return nil
			}
			val = string(data[pos : pos+vlen])
			pos += vlen
		}
		msg.Props[name] = val
	}

	if replyAddr, ok := msg.Props["_replyaddr"]; ok && replyAddr != "" {
		msg.FromIP = replyAddr
		delete(msg.Props, "_replyaddr")
	}
	if replyPort, ok := msg.Props["_replyport"]; ok && replyPort != "" {
		msg.FromPort = replyPort
		delete(msg.Props, "_replyport")
	}
	if msg.Type != "Q" {
		log.Printf("[SOOD] Discovered packet from %s: type=%s service=%s name=%s port=%s",
			msg.FromIP, msg.Type, msg.Props["service_id"], msg.Props["display_name"], msg.Props["http_port"])
	}
	return msg
}

func isLocalAddress(ipStr string) bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				if ipNet.IP.String() == ipStr {
					return true
				}
			}
		}
	}
	return false
}
