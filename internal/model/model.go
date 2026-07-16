// Package model contains persistent and API data structures.
package model

import "time"

// TransportOptions contains the official olcRTC transport settings.
type TransportOptions struct {
	VP8FPS         int    `json:"vp8_fps"`
	VP8Batch       int    `json:"vp8_batch"`
	SEIFPS         int    `json:"sei_fps"`
	SEIBatch       int    `json:"sei_batch"`
	SEIFragment    int    `json:"sei_fragment"`
	SEIAckMS       int    `json:"sei_ack_ms"`
	VideoWidth     int    `json:"video_width"`
	VideoHeight    int    `json:"video_height"`
	VideoFPS       int    `json:"video_fps"`
	VideoBitrate   string `json:"video_bitrate"`
	VideoHW        string `json:"video_hw"`
	VideoCodec     string `json:"video_codec"`
	VideoQRSize    int    `json:"video_qr_size"`
	VideoQRRecover string `json:"video_qr_recovery"`
	VideoTile      int    `json:"video_tile_module"`
	VideoTileRS    int    `json:"video_tile_rs"`
}

// LivenessOptions controls the encrypted control-stream health check.
type LivenessOptions struct {
	Interval string `json:"interval"`
	Timeout  string `json:"timeout"`
	Failures int    `json:"failures"`
}

// TrafficOptions controls upstream traffic shaping.
type TrafficOptions struct {
	MaxPayloadSize int    `json:"max_payload_size"`
	MinDelay       string `json:"min_delay"`
	MaxDelay       string `json:"max_delay"`
}

// Instance is one independently managed olcRTC server process.
type Instance struct {
	ID                  int64            `json:"id"`
	Name                string           `json:"name"`
	Provider            string           `json:"provider"`
	AuthToken           string           `json:"auth_token,omitempty"`
	Transport           string           `json:"transport"`
	RoomID              string           `json:"room_id"`
	RoomChannel         string           `json:"room_channel,omitempty"`
	DNS                 string           `json:"dns"`
	OutboundProxy       string           `json:"outbound_proxy,omitempty"`
	Options             TransportOptions `json:"options"`
	Liveness            LivenessOptions  `json:"liveness"`
	MaxSessionDuration  string           `json:"max_session_duration,omitempty"`
	Traffic             TrafficOptions   `json:"traffic_options"`
	Debug               bool             `json:"debug"`
	ResetPolicy         string           `json:"reset_policy"`
	QuotaBytes          int64            `json:"quota_bytes"`
	ExpiresAt           *time.Time       `json:"expires_at,omitempty"`
	CreatedAt           time.Time        `json:"created_at"`
	UpdatedAt           time.Time        `json:"updated_at"`
	Status              string           `json:"status,omitempty"`
	UptimeSeconds       int64            `json:"uptime_seconds,omitempty"`
	UploadBytes         int64            `json:"upload_bytes"`
	DownloadBytes       int64            `json:"download_bytes"`
	TotalBytes          int64            `json:"total_bytes"`
	LastTrafficAt       *time.Time       `json:"last_traffic_at,omitempty"`
	NetworkIngressBytes int64            `json:"network_ingress_bytes"`
	NetworkEgressBytes  int64            `json:"network_egress_bytes"`
}

// TrafficCounter is the exact payload accounting state for an instance.
type TrafficCounter struct {
	InstanceID      int64      `json:"instance_id"`
	UploadBytes     int64      `json:"upload_bytes"`
	DownloadBytes   int64      `json:"download_bytes"`
	TotalBytes      int64      `json:"total_bytes"`
	PeriodStartedAt time.Time  `json:"period_started_at"`
	LastEventAt     *time.Time `json:"last_event_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// Subscription publishes the standard and compatibility projections.
type Subscription struct {
	ID              int64               `json:"id"`
	Slug            string              `json:"slug"`
	Name            string              `json:"name"`
	RefreshInterval string              `json:"refresh"`
	Color           string              `json:"color,omitempty"`
	Icon            string              `json:"icon,omitempty"`
	Enabled         bool                `json:"enabled"`
	MirrorEnabled   bool                `json:"mirror_enabled"`
	MirrorPublicURL string              `json:"mirror_public_url,omitempty"`
	MirrorStatus    string              `json:"mirror_status,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
	Entries         []SubscriptionEntry `json:"entries,omitempty"`
	UsedBytes       int64               `json:"used_bytes"`
	UploadBytes     int64               `json:"upload_bytes"`
	DownloadBytes   int64               `json:"download_bytes"`
	AvailableBytes  *int64              `json:"available_bytes,omitempty"`
	ExpiresAt       *time.Time          `json:"expires_at,omitempty"`
}

// SubscriptionEntry is linked to an instance or stores an immutable manual URI.
type SubscriptionEntry struct {
	ID                int64      `json:"id"`
	SubscriptionID    int64      `json:"subscription_id"`
	SourceInstanceID  *int64     `json:"source_instance_id,omitempty"`
	RawURI            string     `json:"raw_uri,omitempty"`
	ExclaveCompatible bool       `json:"exclave_compatible"`
	Name              string     `json:"name,omitempty"`
	Color             string     `json:"color,omitempty"`
	Icon              string     `json:"icon,omitempty"`
	IP                string     `json:"ip,omitempty"`
	Comment           string     `json:"comment,omitempty"`
	ManualUsed        *int64     `json:"manual_used,omitempty"`
	ManualAvailable   *int64     `json:"manual_available,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	Enabled           bool       `json:"enabled"`
	SortOrder         int        `json:"sort_order"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// Admin is the single panel administrator.
type Admin struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Session is a hashed cookie-backed admin session.
type Session struct {
	IDHash     string
	AdminID    int64
	CSRFHash   string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	IP         string
	UserAgent  string
}

// AuditEvent is a redacted administrative action.
type AuditEvent struct {
	ID              int64     `json:"id"`
	Action          string    `json:"action"`
	ObjectType      string    `json:"object_type"`
	ObjectID        string    `json:"object_id,omitempty"`
	Result          string    `json:"result"`
	ActorIP         string    `json:"actor_ip,omitempty"`
	DetailsRedacted string    `json:"details,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}
