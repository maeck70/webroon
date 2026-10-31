package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"webroon/internal/roon"
	"webroon/internal/ui"
)

type cachedImage struct {
	data        []byte
	contentType string
}

type Server struct {
	roon            *roon.Client
	mu              sync.Mutex
	activeHierarchy string
	activeTab       string
	lastList        *roon.BrowseList
	lastItems       []roon.BrowseItem
	lastOffset      int
	lastSearch      string

	imgMu    sync.RWMutex
	imgCache map[string]cachedImage
}

func New(rc *roon.Client) *Server {
	return &Server{
		roon:            rc,
		activeHierarchy: "browse",
		activeTab:       "browse",
		imgCache:        make(map[string]cachedImage),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Static files
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Full page entry
	mux.HandleFunc("GET /{$}", s.handleIndex)

	// HTMX Partials
	mux.HandleFunc("GET /ui/status-badge", s.handleStatusBadge)
	mux.HandleFunc("GET /ui/main-on-status", s.handleMainOnStatus)
	mux.HandleFunc("POST /ui/rescan", s.handleRescan)
	mux.HandleFunc("GET /ui/browse", s.handleBrowse)
	mux.HandleFunc("POST /ui/browse/input", s.handleBrowseInput)
	mux.HandleFunc("POST /ui/search", s.handleGlobalSearch)
	mux.HandleFunc("GET /ui/queue", s.handleQueue)
	mux.HandleFunc("GET /ui/now-playing", s.handleNowPlaying)

	// Transport & Zone HTMX actions
	mux.HandleFunc("POST /ui/zone/select", s.handleZoneSelect)
	mux.HandleFunc("POST /ui/transport/control", s.handleTransportControl)
	mux.HandleFunc("POST /ui/transport/seek", s.handleTransportSeek)
	mux.HandleFunc("POST /ui/transport/volume", s.handleTransportVolume)
	mux.HandleFunc("POST /ui/transport/mute", s.handleTransportMute)
	mux.HandleFunc("POST /ui/transport/settings", s.handleTransportSettings)
	mux.HandleFunc("POST /ui/transport/play-from-here", s.handlePlayFromHere)

	// API endpoints (Images & SSE)
	mux.HandleFunc("GET /api/image/{key}", s.handleImage)
	mux.HandleFunc("GET /api/events", s.handleSSE)

	return mux
}

func (s *Server) buildPageState() ui.PageState {
	status, core := s.roon.Status()
	zones, currentZone, currentZoneID := s.roon.GetZones()
	queueItems := s.roon.GetQueue(currentZoneID)

	s.mu.Lock()
	defer s.mu.Unlock()

	return ui.PageState{
		Status:          status,
		Core:            core,
		Zones:           zones,
		CurrentZone:     currentZone,
		CurrentZoneID:   currentZoneID,
		ActiveHierarchy: s.activeHierarchy,
		ActiveTab:       s.activeTab,
		SearchQuery:     s.lastSearch,
		BrowseList:      s.lastList,
		BrowseItems:     s.lastItems,
		BrowseOffset:    s.lastOffset,
		QueueItems:      queueItems,
	}
}

func (s *Server) ensureInitialBrowseLoaded(ctx context.Context) {
	status, _ := s.roon.Status()
	if status != roon.StatusConnected {
		return
	}
	s.mu.Lock()
	hasList := s.lastList != nil
	hierarchy := s.activeHierarchy
	s.mu.Unlock()

	if !hasList {
		_, _, _ = s.execBrowseAndLoad(ctx, roon.BrowseOpts{
			Hierarchy: hierarchy,
		}, 0)
	}
}

func (s *Server) execBrowseAndLoad(ctx context.Context, opts roon.BrowseOpts, offset int) (string, bool, error) {
	if opts.Hierarchy == "" {
		opts.Hierarchy = "browse"
	}

	var flashMsg string
	var flashErr bool

	// If we are performing a browse action (or initial load without offset-only paging)
	if opts.ItemKey != "" || opts.PopAll || opts.PopLevels > 0 || opts.RefreshList || opts.Input != "" || offset == 0 {
		res, err := s.roon.Browse(ctx, opts)
		if err != nil {
			return err.Error(), true, err
		}
		if res.Action == "message" {
			flashMsg = res.Message
			flashErr = res.IsError
		}
		if res.List != nil && offset == 0 && res.List.DisplayOffset > 0 {
			offset = res.List.DisplayOffset
		}
	}

	loadRes, err := s.roon.Load(ctx, roon.LoadOpts{
		Hierarchy:        opts.Hierarchy,
		Offset:           offset,
		Count:            100,
		SetDisplayOffset: offset,
	})
	if err != nil {
		return flashMsg, flashErr, err
	}

	s.mu.Lock()
	s.activeHierarchy = opts.Hierarchy
	s.activeTab = "browse"
	s.lastList = loadRes.List
	s.lastItems = loadRes.Items
	s.lastOffset = loadRes.Offset
	s.mu.Unlock()

	return flashMsg, flashErr, nil
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	s.ensureInitialBrowseLoaded(ctx)
	state := s.buildPageState()
	_ = ui.Layout(state).Render(r.Context(), w)
}

func (s *Server) handleStatusBadge(w http.ResponseWriter, r *http.Request) {
	status, core := s.roon.Status()
	_ = ui.StatusBadge(status, core).Render(r.Context(), w)
}

func (s *Server) handleMainOnStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	s.ensureInitialBrowseLoaded(ctx)
	state := s.buildPageState()
	_ = ui.MainContent(state).Render(r.Context(), w)
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	s.roon.Start()
	state := s.buildPageState()
	_ = ui.MainContent(state).Render(r.Context(), w)
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	q := r.URL.Query()
	hierarchy := q.Get("hierarchy")
	if hierarchy == "" {
		s.mu.Lock()
		hierarchy = s.activeHierarchy
		s.mu.Unlock()
	}

	itemKey := q.Get("item_key")
	popAll := q.Get("pop_all") == "1"
	popLevels, _ := strconv.Atoi(q.Get("pop_levels"))
	refresh := q.Get("refresh") == "1"
	offset, _ := strconv.Atoi(q.Get("offset"))

	var flashMsg string
	var flashErr bool

	// If only paging through the existing list
	if itemKey == "" && !popAll && popLevels == 0 && !refresh && q.Has("offset") {
		loadRes, err := s.roon.Load(ctx, roon.LoadOpts{
			Hierarchy:        hierarchy,
			Offset:           offset,
			Count:            100,
			SetDisplayOffset: offset,
		})
		if err != nil {
			flashMsg = err.Error()
			flashErr = true
		} else {
			s.mu.Lock()
			s.activeHierarchy = hierarchy
			s.activeTab = "browse"
			s.lastList = loadRes.List
			s.lastItems = loadRes.Items
			s.lastOffset = loadRes.Offset
			s.mu.Unlock()
		}
	} else {
		msg, isErr, err := s.execBrowseAndLoad(ctx, roon.BrowseOpts{
			Hierarchy:   hierarchy,
			ItemKey:     itemKey,
			PopAll:      popAll,
			PopLevels:   popLevels,
			RefreshList: refresh,
		}, offset)
		flashMsg = msg
		flashErr = isErr
		if err != nil && flashMsg == "" {
			flashMsg = err.Error()
			flashErr = true
		}
	}

	state := s.buildPageState()
	state.FlashMessage = flashMsg
	state.FlashIsError = flashErr
	_ = ui.BrowseView(state).Render(r.Context(), w)
}

func (s *Server) handleBrowseInput(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	_ = r.ParseForm()
	hierarchy := r.FormValue("hierarchy")
	itemKey := r.FormValue("item_key")
	input := strings.TrimSpace(r.FormValue("input"))

	flashMsg, flashErr, err := s.execBrowseAndLoad(ctx, roon.BrowseOpts{
		Hierarchy: hierarchy,
		ItemKey:   itemKey,
		Input:     input,
	}, 0)
	if err != nil && flashMsg == "" {
		flashMsg = err.Error()
		flashErr = true
	}

	state := s.buildPageState()
	state.FlashMessage = flashMsg
	state.FlashIsError = flashErr
	_ = ui.BrowseView(state).Render(r.Context(), w)
}

func (s *Server) handleGlobalSearch(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	_ = r.ParseForm()
	query := strings.TrimSpace(r.FormValue("q"))
	if query == "" {
		state := s.buildPageState()
		_ = ui.BrowseView(state).Render(r.Context(), w)
		return
	}

	loadRes, err := s.roon.GlobalSearch(ctx, query)
	var flashMsg string
	var flashErr bool
	if err != nil {
		flashMsg = err.Error()
		flashErr = true
	} else if loadRes != nil {
		s.mu.Lock()
		s.activeHierarchy = "browse"
		s.activeTab = "browse"
		s.lastSearch = query
		s.lastList = loadRes.List
		s.lastItems = loadRes.Items
		s.lastOffset = loadRes.Offset
		s.mu.Unlock()
	}

	state := s.buildPageState()
	state.FlashMessage = flashMsg
	state.FlashIsError = flashErr
	_ = ui.BrowseView(state).Render(r.Context(), w)
}

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.activeTab = "queue"
	s.mu.Unlock()

	state := s.buildPageState()
	_ = ui.QueueView(state).Render(r.Context(), w)
}

func (s *Server) handleNowPlaying(w http.ResponseWriter, r *http.Request) {
	state := s.buildPageState()
	_ = ui.NowPlayingBar(state).Render(r.Context(), w)
}

func (s *Server) handleZoneSelect(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	zoneID := r.FormValue("zone_id")
	if zoneID != "" {
		s.roon.SetCurrentZone(zoneID)
	}
	state := s.buildPageState()
	_ = ui.NowPlayingBar(state).Render(r.Context(), w)
}

func (s *Server) handleTransportControl(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_ = r.ParseForm()
	zoneID := r.FormValue("zone_id")
	control := r.FormValue("control")
	_ = s.roon.Control(ctx, zoneID, control)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTransportSeek(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_ = r.ParseForm()
	zoneID := r.FormValue("zone_id")
	sec, err := strconv.Atoi(r.FormValue("seconds"))
	if err == nil {
		_ = s.roon.Seek(ctx, zoneID, "absolute", sec)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTransportVolume(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_ = r.ParseForm()
	outputID := r.FormValue("output_id")
	how := r.FormValue("how")
	if how == "" {
		how = "absolute"
	}
	val, err := strconv.ParseFloat(r.FormValue("value"), 64)
	if err == nil {
		_ = s.roon.ChangeVolume(ctx, outputID, how, val)
	}
	state := s.buildPageState()
	_ = ui.NowPlayingBar(state).Render(r.Context(), w)
}

func (s *Server) handleTransportMute(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_ = r.ParseForm()
	outputID := r.FormValue("output_id")
	how := r.FormValue("how")
	_ = s.roon.Mute(ctx, outputID, how)

	state := s.buildPageState()
	_ = ui.NowPlayingBar(state).Render(r.Context(), w)
}

func (s *Server) handleTransportSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_ = r.ParseForm()
	zoneID := r.FormValue("zone_id")
	setting := r.FormValue("setting")
	valStr := r.FormValue("value")

	settings := make(map[string]any)
	switch setting {
	case "shuffle", "auto_radio":
		settings[setting] = (valStr == "true")
	case "loop":
		settings["loop"] = valStr
	}
	if len(settings) > 0 {
		_ = s.roon.ChangeSettings(ctx, zoneID, settings)
	}

	state := s.buildPageState()
	_ = ui.NowPlayingBar(state).Render(r.Context(), w)
}

func (s *Server) handlePlayFromHere(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_ = r.ParseForm()
	zoneID := r.FormValue("zone_id")
	qID, err := strconv.Atoi(r.FormValue("queue_item_id"))
	if err == nil {
		_ = s.roon.PlayFromHere(ctx, zoneID, qID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.NotFound(w, r)
		return
	}
	scale := r.URL.Query().Get("scale")
	if scale == "" {
		scale = "fit"
	}
	width, _ := strconv.Atoi(r.URL.Query().Get("width"))
	height, _ := strconv.Atoi(r.URL.Query().Get("height"))
	if width <= 0 {
		width = 300
	}
	if height <= 0 {
		height = 300
	}

	cacheKey := fmt.Sprintf("%s:%s:%dx%d", key, scale, width, height)
	s.imgMu.RLock()
	cached, ok := s.imgCache[cacheKey]
	s.imgMu.RUnlock()
	if ok {
		w.Header().Set("Content-Type", cached.contentType)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(cached.data)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	data, ct, err := s.roon.FetchImage(ctx, key, scale, width, height)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	s.imgMu.Lock()
	if len(s.imgCache) > 600 {
		s.imgCache = make(map[string]cachedImage)
	}
	s.imgCache[cacheKey] = cachedImage{data: data, contentType: ct}
	s.imgMu.Unlock()

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsub := s.roon.SubscribeEvents()
	defer unsub()

	// Send initial status event
	status, _ := s.roon.Status()
	initBytes, _ := json.Marshal(map[string]any{"status": status})
	fmt.Fprintf(w, "event: status\ndata: %s\n\n", string(initBytes))
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			switch ev.Type {
			case roon.EventStatusChanged:
				st, _ := s.roon.Status()
				b, _ := json.Marshal(map[string]any{"status": st})
				fmt.Fprintf(w, "event: status\ndata: %s\n\n", string(b))
			case roon.EventZonesChanged:
				fmt.Fprintf(w, "event: zones\ndata: {}\n\n")
			case roon.EventSeekChanged:
				_, curZone, _ := s.roon.GetZones()
				seekPos := ui.SeekValue(curZone)
				b, _ := json.Marshal(map[string]any{"seek_position": seekPos})
				fmt.Fprintf(w, "event: seek\ndata: %s\n\n", string(b))
			case roon.EventQueueChanged:
				fmt.Fprintf(w, "event: queue\ndata: {}\n\n")
			}
			flusher.Flush()
		}
	}
}
