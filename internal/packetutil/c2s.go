package packetutil

import (
	"fmt"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// c2sParsers re-exposes aolib's client→server decoders so AsyncAO can
// reconstruct a typed packet from positional args and re-encode it (to Fanta
// or JSON) through aolib's typed encoder.
//
// MS is deliberately absent: aolib's MSToServer models the canonical 26-field
// layout, while AsyncAO's MS wire carries 32 fields (the multi-pair details
// other_name/other_emote/other_offset/other_flip plus blipname/slide). Routing
// MS through this map would silently drop those six fields, so MS is left to a
// dedicated both-wire codec (see RegisterCodec); until then BuildWire frames MS
// positionally and preserves every field.
var c2sParsers = map[string]func([]string) (aolib.Outgoing, error){
	"askchaa": func(b []string) (aolib.Outgoing, error) { return aolib.ParseAskchaa(b) },
	"CC":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseCC(b) },
	"CH":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseCH(b) },
	"CT":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseCTToServer(b) },
	"DE":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseDE(b) },
	"EE":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseEE(b) },
	"HI":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseHI(b) },
	"HP":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseHPToServer(b) },
	"ID":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseIDToServer(b) },
	"MA":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseMA(b) },
	"MC":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseMCToServer(b) },
	"PE":      func(b []string) (aolib.Outgoing, error) { return aolib.ParsePE(b) },
	"RC":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseRC(b) },
	"RD":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseRD(b) },
	"RM":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseRM(b) },
	"RT":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseRTToServer(b) },
	"ZZ":      func(b []string) (aolib.Outgoing, error) { return aolib.ParseZZToServer(b) },
}

// BuildWire encodes a type-erased (header, positional-args) client→server
// packet to its wire form. args are UNESCAPED positional fields (exactly what
// protocol.Packet holds). Custom headers route through their codec; canonical
// headers are reconstructed from the args and encoded by aolib (correct JSON
// field names + enum strings); headers with no model are framed positionally
// (Fanta) or as a bare envelope (JSON).
func BuildWire(header string, args []string, mode aolib.WireMode) ([]byte, error) {
	escaped := EscapeAll(args)
	if c, ok := customCodecs[header]; ok {
		p, err := c.DecodeFanta(escaped)
		if err != nil {
			return nil, err
		}
		return encodeCustom(header, p, c, mode)
	}
	if parse, ok := c2sParsers[header]; ok {
		p, err := parse(escaped)
		if err != nil {
			return nil, err
		}
		return aolib.Encode(p, mode)
	}
	switch mode {
	case aolib.WireFanta:
		return frameFanta(header, escaped), nil
	case aolib.WireJSON:
		return bareEnvelope(header), nil
	default:
		return nil, fmt.Errorf("packetutil: unknown wire mode %d", mode)
	}
}
