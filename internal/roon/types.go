package roon

// CoreInfo holds metadata about a discovered/registered Roon Core.
type CoreInfo struct {
	CoreID         string `json:"core_id"`
	DisplayName    string `json:"display_name"`
	DisplayVersion string `json:"display_version"`
	Token          string `json:"token,omitempty"`
	Host           string `json:"host"`
	Port           string `json:"port"`
}

// ConnectionStatus represents the current state of the Roon Core connection.
type ConnectionStatus string

const (
	StatusDiscovering       ConnectionStatus = "discovering"
	StatusWaitingPermission ConnectionStatus = "waiting_permission"
	StatusConnected         ConnectionStatus = "connected"
)

// Zone represents a Roon playback zone.
type Zone struct {
	ZoneID              string        `json:"zone_id"`
	DisplayName         string        `json:"display_name"`
	Outputs             []Output      `json:"outputs"`
	State               string        `json:"state"` // playing, paused, loading, stopped
	SeekPosition        *int          `json:"seek_position,omitempty"`
	QueueItemsRemaining int           `json:"queue_items_remaining,omitempty"`
	QueueTimeRemaining  int           `json:"queue_time_remaining,omitempty"`
	IsPreviousAllowed   bool          `json:"is_previous_allowed"`
	IsNextAllowed       bool          `json:"is_next_allowed"`
	IsPauseAllowed      bool          `json:"is_pause_allowed"`
	IsPlayAllowed       bool          `json:"is_play_allowed"`
	IsSeekAllowed       bool          `json:"is_seek_allowed"`
	Settings            *ZoneSettings `json:"settings,omitempty"`
	NowPlaying          *NowPlaying   `json:"now_playing,omitempty"`
}

// ZoneSettings holds loop, shuffle, and Roon Radio state for a zone.
type ZoneSettings struct {
	Loop      string `json:"loop"` // loop, loop_one, disabled
	Shuffle   bool   `json:"shuffle"`
	AutoRadio bool   `json:"auto_radio"`
}

// Output represents an audio output within a Zone.
type Output struct {
	OutputID       string          `json:"output_id"`
	ZoneID         string          `json:"zone_id"`
	DisplayName    string          `json:"display_name"`
	State          string          `json:"state"`
	Volume         *OutputVolume   `json:"volume,omitempty"`
	SourceControls []SourceControl `json:"source_controls,omitempty"`
}

// OutputVolume describes the volume capabilities and state of an Output.
type OutputVolume struct {
	Type       string  `json:"type"` // number, db, incremental
	Min        float64 `json:"min"`
	Max        float64 `json:"max"`
	Value      float64 `json:"value"`
	Step       float64 `json:"step"`
	IsMuted    bool    `json:"is_muted"`
	HardLimitMin float64 `json:"hard_limit_min,omitempty"`
	HardLimitMax float64 `json:"hard_limit_max,omitempty"`
	SoftLimit  float64 `json:"soft_limit,omitempty"`
}

// SourceControl represents standby/source switching controls on an Output.
type SourceControl struct {
	ControlKey      string `json:"control_key"`
	DisplayName     string `json:"display_name"`
	Status          string `json:"status"` // selected, deselected, standby, indeterminate
	SupportsStandby bool   `json:"supports_standby"`
}

// NowPlaying holds metadata about the currently playing media in a Zone.
type NowPlaying struct {
	SeekPosition *int       `json:"seek_position,omitempty"`
	Length       int        `json:"length,omitempty"`
	ImageKey     string     `json:"image_key,omitempty"`
	OneLine      OneLine    `json:"one_line"`
	TwoLine      TwoLine    `json:"two_line"`
	ThreeLine    ThreeLine  `json:"three_line"`
}

type OneLine struct {
	Line1 string `json:"line1"`
}

type TwoLine struct {
	Line1 string `json:"line1"`
	Line2 string `json:"line2,omitempty"`
}

type ThreeLine struct {
	Line1 string `json:"line1"`
	Line2 string `json:"line2,omitempty"`
	Line3 string `json:"line3,omitempty"`
}

// QueueItem represents an entry in a zone's playback queue.
type QueueItem struct {
	QueueItemID int       `json:"queue_item_id"`
	Length      int       `json:"length"`
	ImageKey    string    `json:"image_key,omitempty"`
	OneLine     OneLine   `json:"one_line"`
	TwoLine     TwoLine   `json:"two_line"`
	ThreeLine   ThreeLine `json:"three_line"`
}

// BrowseOpts specifies parameters for com.roonlabs.browse:1/browse.
type BrowseOpts struct {
	Hierarchy        string `json:"hierarchy"`
	MultiSessionKey  string `json:"multi_session_key,omitempty"`
	ItemKey          string `json:"item_key,omitempty"`
	Input            string `json:"input,omitempty"`
	ZoneOrOutputID   string `json:"zone_or_output_id,omitempty"`
	PopAll           bool   `json:"pop_all,omitempty"`
	PopLevels        int    `json:"pop_levels,omitempty"`
	RefreshList      bool   `json:"refresh_list,omitempty"`
	SetDisplayOffset int    `json:"set_display_offset,omitempty"`
}

// BrowseResult is returned by com.roonlabs.browse:1/browse.
type BrowseResult struct {
	Action  string      `json:"action"` // list, message, none, replace_item, remove_item
	Item    *BrowseItem `json:"item,omitempty"`
	List    *BrowseList `json:"list,omitempty"`
	Message string      `json:"message,omitempty"`
	IsError bool        `json:"is_error,omitempty"`
}

// LoadOpts specifies parameters for com.roonlabs.browse:1/load.
type LoadOpts struct {
	Hierarchy        string `json:"hierarchy"`
	MultiSessionKey  string `json:"multi_session_key,omitempty"`
	Level            *int   `json:"level,omitempty"`
	Offset           int    `json:"offset"`
	Count            int    `json:"count,omitempty"`
	SetDisplayOffset int    `json:"set_display_offset"`
}

// LoadResult is returned by com.roonlabs.browse:1/load.
type LoadResult struct {
	Items  []BrowseItem `json:"items"`
	Offset int          `json:"offset"`
	List   *BrowseList  `json:"list"`
}

// BrowseList describes a level in Roon's browse stack.
type BrowseList struct {
	Title         string `json:"title"`
	Count         int    `json:"count"`
	Subtitle      string `json:"subtitle,omitempty"`
	ImageKey      string `json:"image_key,omitempty"`
	Level         int    `json:"level"`
	DisplayOffset int    `json:"display_offset,omitempty"`
	Hint          string `json:"hint,omitempty"` // null, action_list
}

// BrowseItem describes an item within a BrowseList.
type BrowseItem struct {
	Title       string       `json:"title"`
	Subtitle    string       `json:"subtitle,omitempty"`
	ImageKey    string       `json:"image_key,omitempty"`
	ItemKey     string       `json:"item_key,omitempty"`
	Hint        string       `json:"hint,omitempty"` // null, action, action_list, list, header
	InputPrompt *InputPrompt `json:"input_prompt,omitempty"`
}

// InputPrompt indicates that selecting this item requires text input from the user (e.g. Search).
type InputPrompt struct {
	Prompt     string `json:"prompt"`
	Action     string `json:"action"`
	Value      string `json:"value,omitempty"`
	IsPassword bool   `json:"is_password,omitempty"`
}

// PersistedState stores pairing tokens and user preferences across restarts.
type PersistedState struct {
	Tokens        map[string]string `json:"tokens"`
	PairedCoreID  string            `json:"paired_core_id,omitempty"`
	CurrentZoneID string            `json:"current_zone_id,omitempty"`
}
