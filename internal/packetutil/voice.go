package packetutil

// Voice-chat packets (VS_*). Removed from the canonical aolib spec (aa8d0fb);
// AsyncAO keeps them as a client extension, registered both-wire via
// RegisterCodec so the JSON wire carries them too (LemmyAO/Nyathena speak the
// same shapes). Wire contract:
//
//	S→C  VS_CAPS#<enabled>#<ptt_only>#<max_peers>#<codec>#<sample_rate>#<frame_ms>#<max_frame_bytes>#%
//	S→C  VS_PEERS#<csv_uids>#%
//	S→C  VS_JOIN#<uid>#%   VS_LEAVE#<uid>#%   VS_SPEAK#<uid>#<on>#%   VS_AUDIO#<from_uid>#<b64>#%
//	C→S  VS_JOIN#%         VS_LEAVE#%         VS_SPEAK#<on>#%         VS_FRAME#<b64>#%

import (
	"strings"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// VS_CAPS is the server's voice capability advertisement (server→client).
type VS_CAPS struct {
	Enabled       bool   `json:"enabled"`
	PttOnly       bool   `json:"pttOnly"`
	MaxPeers      int    `json:"maxPeers"`
	Codec         string `json:"codec"`
	SampleRate    int    `json:"sampleRate"`
	FrameMs       int    `json:"frameMs"`
	MaxFrameBytes int    `json:"maxFrameBytes"`
}

func (p *VS_CAPS) Header() string { return "VS_CAPS" }

func (p *VS_CAPS) Args() []string {
	return []string{
		BoolToWire(p.Enabled),
		BoolToWire(p.PttOnly),
		Itoa(p.MaxPeers),
		aolib.EscapeFanta(p.Codec),
		Itoa(p.SampleRate),
		Itoa(p.FrameMs),
		Itoa(p.MaxFrameBytes),
	}
}

func ParseVS_CAPS(body []string) (*VS_CAPS, error) {
	p := &VS_CAPS{}
	p.Enabled = WireToBool(GetStr(body, 0))
	p.PttOnly = WireToBool(GetStr(body, 1))
	p.MaxPeers = AtoiOrZero(GetStr(body, 2))
	p.Codec = aolib.UnescapeFanta(GetStr(body, 3))
	p.SampleRate = AtoiOrZero(GetStr(body, 4))
	p.FrameMs = AtoiOrZero(GetStr(body, 5))
	p.MaxFrameBytes = AtoiOrZero(GetStr(body, 6))
	return p, nil
}

// VS_PEERS is the server's comma-separated roster of voice peers (server→client).
type VS_PEERS struct {
	Uids []int `json:"uids"`
}

func (p *VS_PEERS) Header() string { return "VS_PEERS" }

func (p *VS_PEERS) Args() []string {
	if len(p.Uids) == 0 {
		return nil
	}
	parts := make([]string, len(p.Uids))
	for i, u := range p.Uids {
		parts[i] = Itoa(u)
	}
	return []string{strings.Join(parts, ",")}
}

func ParseVS_PEERS(body []string) (*VS_PEERS, error) {
	p := &VS_PEERS{}
	if s := GetStr(body, 0); s != "" {
		for _, f := range strings.Split(s, ",") {
			if f = strings.TrimSpace(f); f != "" {
				p.Uids = append(p.Uids, AtoiOrZero(f))
			}
		}
	}
	return p, nil
}

// VS_JOINToClient is the server's broadcast that a peer joined (server→client).
type VS_JOINToClient struct {
	UID int `json:"uid"`
}

func (p *VS_JOINToClient) Header() string { return "VS_JOIN" }

func (p *VS_JOINToClient) Args() []string { return []string{Itoa(p.UID)} }

func ParseVS_JOINToClient(body []string) (*VS_JOINToClient, error) {
	return &VS_JOINToClient{UID: AtoiOrZero(GetStr(body, 0))}, nil
}

// VS_JOINToServer is a client's join request (client→server). No fields.
type VS_JOINToServer struct{}

func (p *VS_JOINToServer) Header() string { return "VS_JOIN" }

func (p *VS_JOINToServer) Args() []string { return nil }

// VS_LEAVEToClient is the server's broadcast that a peer left (server→client).
type VS_LEAVEToClient struct {
	UID int `json:"uid"`
}

func (p *VS_LEAVEToClient) Header() string { return "VS_LEAVE" }

func (p *VS_LEAVEToClient) Args() []string { return []string{Itoa(p.UID)} }

func ParseVS_LEAVEToClient(body []string) (*VS_LEAVEToClient, error) {
	return &VS_LEAVEToClient{UID: AtoiOrZero(GetStr(body, 0))}, nil
}

// VS_LEAVEToServer is a client's leave request (client→server). No fields.
type VS_LEAVEToServer struct{}

func (p *VS_LEAVEToServer) Header() string { return "VS_LEAVE" }

func (p *VS_LEAVEToServer) Args() []string { return nil }

// VS_SPEAKToClient is the server's broadcast of a speaking-state change.
type VS_SPEAKToClient struct {
	UID int  `json:"uid"`
	On  bool `json:"on"`
}

func (p *VS_SPEAKToClient) Header() string { return "VS_SPEAK" }

func (p *VS_SPEAKToClient) Args() []string { return []string{Itoa(p.UID), BoolToWire(p.On)} }

func ParseVS_SPEAKToClient(body []string) (*VS_SPEAKToClient, error) {
	return &VS_SPEAKToClient{UID: AtoiOrZero(GetStr(body, 0)), On: WireToBool(GetStr(body, 1))}, nil
}

// VS_SPEAKToServer is a client's speaking-state change (client→server).
type VS_SPEAKToServer struct {
	On bool `json:"on"`
}

func (p *VS_SPEAKToServer) Header() string { return "VS_SPEAK" }

func (p *VS_SPEAKToServer) Args() []string { return []string{BoolToWire(p.On)} }

// VS_AUDIO relays one Opus frame to a peer (server→client).
type VS_AUDIO struct {
	FromUID int    `json:"fromUid"`
	Payload string `json:"payload"`
}

func (p *VS_AUDIO) Header() string { return "VS_AUDIO" }

func (p *VS_AUDIO) Args() []string { return []string{Itoa(p.FromUID), aolib.EscapeFanta(p.Payload)} }

func ParseVS_AUDIO(body []string) (*VS_AUDIO, error) {
	return &VS_AUDIO{FromUID: AtoiOrZero(GetStr(body, 0)), Payload: aolib.UnescapeFanta(GetStr(body, 1))}, nil
}

// VS_FRAME carries one Opus frame upstream (client→server).
type VS_FRAME struct {
	Payload string `json:"payload"`
}

func (p *VS_FRAME) Header() string { return "VS_FRAME" }

func (p *VS_FRAME) Args() []string { return []string{aolib.EscapeFanta(p.Payload)} }

func ParseVS_FRAME(body []string) (*VS_FRAME, error) {
	return &VS_FRAME{Payload: aolib.UnescapeFanta(GetStr(body, 0))}, nil
}
