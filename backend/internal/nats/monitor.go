package nats

// Types below mirror the subset of the NATS server monitoring API
// (https://docs.nats.io/running-a-nats-service/nats_admin/monitoring) this
// dashboard reads. Unlisted fields in the real /varz and /jsz responses are
// dropped silently by json.Unmarshal - only what the UI shows is declared.

type ClusterInfo struct {
	Name string `json:"name,omitempty"`
}

type Varz struct {
	ServerID    string      `json:"server_id"`
	Version     string      `json:"version"`
	Uptime      string      `json:"uptime"`
	Connections int         `json:"connections"`
	InMsgs      int64       `json:"in_msgs"`
	OutMsgs     int64       `json:"out_msgs"`
	InBytes     int64       `json:"in_bytes"`
	OutBytes    int64       `json:"out_bytes"`
	MaxPayload  int64       `json:"max_payload"`
	Cluster     ClusterInfo `json:"cluster"`
	Gateway     ClusterInfo `json:"gateway"`
	LeafNodes   int         `json:"leafnodes"`
}

type Healthz struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type ConsumerDetail struct {
	Name           string `json:"name"`
	NumAckPending  int    `json:"num_ack_pending"`
	NumRedelivered int    `json:"num_redelivered"`
	NumWaiting     int    `json:"num_waiting"`
	NumPending     uint64 `json:"num_pending"`
}

type StreamState struct {
	Messages uint64 `json:"messages"`
	Bytes    uint64 `json:"bytes"`
	FirstSeq uint64 `json:"first_seq"`
	LastSeq  uint64 `json:"last_seq"`
}

// StreamConfig is the desired-state subset of a stream's config, as returned
// by /jsz?config=true. It's what tells a KV or Object Store stream apart from
// a plain one (by name convention) and shows retention/storage at a glance.
type StreamConfig struct {
	Subjects  []string `json:"subjects,omitempty"`
	Retention string   `json:"retention,omitempty"`
	Storage   string   `json:"storage,omitempty"`
	MaxAge    int64    `json:"max_age,omitempty"`
	MaxBytes  int64    `json:"max_bytes,omitempty"`
	MaxMsgs   int64    `json:"max_msgs,omitempty"`
}

type StreamDetail struct {
	Name      string           `json:"name"`
	Config    StreamConfig     `json:"config"`
	State     StreamState      `json:"state"`
	Consumers []ConsumerDetail `json:"consumer_detail"`
}

type AccountDetail struct {
	Name    string         `json:"name"`
	Streams []StreamDetail `json:"stream_detail"`
}

type Jsz struct {
	Streams     int             `json:"streams"`
	Consumers   int             `json:"consumers"`
	Messages    uint64          `json:"messages"`
	Bytes       uint64          `json:"bytes"`
	AccountList []AccountDetail `json:"account_details"`
}

// ConnInfo is one client connection, as listed by /connz.
type ConnInfo struct {
	Cid           uint64 `json:"cid"`
	IP            string `json:"ip"`
	Port          int    `json:"port"`
	Name          string `json:"name,omitempty"`
	Account       string `json:"account,omitempty"`
	Lang          string `json:"lang,omitempty"`
	Version       string `json:"version,omitempty"`
	Uptime        string `json:"uptime"`
	Subscriptions int    `json:"subscriptions"`
	InMsgs        int64  `json:"in_msgs"`
	OutMsgs       int64  `json:"out_msgs"`
	InBytes       int64  `json:"in_bytes"`
	OutBytes      int64  `json:"out_bytes"`
}

type Connz struct {
	NumConnections int        `json:"num_connections"`
	Connections    []ConnInfo `json:"connections"`
}
