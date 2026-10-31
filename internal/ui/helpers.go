package ui

import (
	"fmt"
	"net/url"
	"strings"

	"webroon/internal/roon"
)

// PageState holds the complete state needed to render the full layout or partials.
type PageState struct {
	Status           roon.ConnectionStatus
	Core             *roon.CoreInfo
	Zones            []roon.Zone
	CurrentZone      *roon.Zone
	CurrentZoneID    string
	ActiveHierarchy  string
	SearchQuery      string
	BrowseList       *roon.BrowseList
	BrowseItems      []roon.BrowseItem
	BrowseOffset     int
	FlashMessage     string
	FlashIsError     bool
	QueueItems       []roon.QueueItem
	ActiveTab        string // "browse" or "queue"
}

// FormatSeconds formats seconds into m:ss or h:mm:ss.
func FormatSeconds(sec int) string {
	if sec < 0 {
		sec = 0
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// ImageURL returns the local proxy URL for a Roon image_key.
func ImageURL(imageKey string, size int) string {
	if imageKey == "" {
		return ""
	}
	return fmt.Sprintf("/api/image/%s?scale=fit&width=%d&height=%d", url.PathEscape(imageKey), size, size)
}

// SeekValue returns the current seek position in seconds for a Zone.
func SeekValue(z *roon.Zone) int {
	if z == nil {
		return 0
	}
	if z.NowPlaying != nil && z.NowPlaying.SeekPosition != nil {
		return *z.NowPlaying.SeekPosition
	}
	if z.SeekPosition != nil {
		return *z.SeekPosition
	}
	return 0
}

// SeekPercent calculates the 0..100 progress percentage for the current track.
func SeekPercent(z *roon.Zone) float64 {
	if z == nil || z.NowPlaying == nil || z.NowPlaying.Length <= 0 {
		return 0
	}
	pos := SeekValue(z)
	pct := (float64(pos) / float64(z.NowPlaying.Length)) * 100.0
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// ShouldRenderGrid decides whether a BrowseList and its items are best displayed as a visual card grid.
func ShouldRenderGrid(hierarchy string, list *roon.BrowseList, items []roon.BrowseItem) bool {
	if list == nil || list.Hint == "action_list" || len(items) == 0 {
		return false
	}
	// If it's an album track list or action menu, use list layout
	withImages := 0
	actionItems := 0
	for _, it := range items {
		if it.ImageKey != "" {
			withImages++
		}
		if it.Hint == "action" || it.Hint == "action_list" {
			actionItems++
		}
	}
	// If majority of items have images and it's not primarily an action list
	if withImages > 0 && float64(withImages)/float64(len(items)) >= 0.5 && actionItems <= 2 {
		return true
	}
	return false
}

// IsPlayableAction checks if an item title/hint looks like a primary playback action.
func IsPlayableAction(item roon.BrowseItem) bool {
	if item.Hint == "action" || item.Hint == "action_list" {
		t := strings.ToLower(item.Title)
		return strings.Contains(t, "play") || strings.Contains(t, "queue") || strings.Contains(t, "radio") || strings.Contains(t, "shuffle")
	}
	return false
}

// FormatVolume formats an OutputVolume value nicely.
func FormatVolume(v *roon.OutputVolume) string {
	if v == nil {
		return ""
	}
	if v.Type == "db" {
		return fmt.Sprintf("%.1f dB", v.Value)
	}
	return fmt.Sprintf("%.0f", v.Value)
}
