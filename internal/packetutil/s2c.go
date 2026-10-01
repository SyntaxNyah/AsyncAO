package packetutil

import (
	"fmt"
	"strings"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// Decoder decodes server→client wire frames (Fanta or JSON, auto-detected) into
// their header and UNESCAPED positional fields — the exact shape AsyncAO's
// existing HandlePacket switch consumes. JSON is decoded through a
// ServerSession so aolib applies schema defaults and validation, and the typed
// value is folded back to positional args via Args().
type Decoder struct {
	sess   *aolib.ServerSession
	header string
	val    any
	err    error
}

// NewDecoder returns a Decoder. It is not safe for concurrent use; the
// connection's read loop is its only caller.
func NewDecoder() *Decoder {
	d := &Decoder{}
	d.sess = aolib.NewServer(aolib.SessionConfig{
		OnUnhandled: func(header string, p any) {
			d.header = header
			d.val = p
		},
		OnDecodeError: func(header string, err error, wire []byte) { d.err = err },
		OnMalformedFrame: func(err error, wire []byte) {
			d.err = err
		},
		OnUnknownHeader: func(header string, wire []byte) {
			d.err = fmt.Errorf("packetutil: unknown header %q", header)
		},
	})
	return d
}

// DecodeBody decodes one frame into its header and unescaped positional fields.
func (d *Decoder) DecodeBody(raw []byte) (string, []string, error) {
	if !IsJSON(raw) {
		pkt, err := aolib.NewPacket(strings.TrimSuffix(string(raw), "%"))
		if err != nil {
			return "", nil, err
		}
		return pkt.Header, UnescapeAll(pkt.Body), nil
	}

	d.header, d.val, d.err = "", nil, nil
	d.sess.Receive(raw)
	if d.err != nil {
		return "", nil, d.err
	}
	if d.val == nil {
		return d.header, nil, nil
	}
	if o, ok := d.val.(aolib.Outgoing); ok {
		return d.header, UnescapeAll(o.Args()), nil
	}
	if c, ok := customCodecs[d.header]; ok {
		args, err := c.EncodeFanta(d.val)
		if err != nil {
			return "", nil, err
		}
		return d.header, UnescapeAll(args), nil
	}
	return d.header, nil, nil
}
