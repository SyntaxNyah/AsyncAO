package protocol

import (
	"strings"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// voiceHeaders are the custom (non-canonical) headers AsyncAO registers codecs
// for; aolib models only the canonical spec, so these ride its custom channel.
var voiceHeaders = []string{"VS_CAPS", "VS_PEERS", "VS_AUDIO", "VS_FRAME", "VS_JOIN", "VS_LEAVE", "VS_SPEAK"}

// registerInbound wires the session's typed On*/OnCustom hooks so every inbound
// packet becomes a Packet on the conn's backlog. Canonical packets decode
// through aolib's typed structs; only the VS_* custom headers use codecs.
func (c *Conn) registerInbound() {
	s := c.sess
	enq := func(p aolib.Outgoing) {
		c.enqueue(Packet{Header: p.Header(), Fields: unescapeArgs(p.Args()), typed: p})
	}

	s.OnARUP(func(p *aolib.ARUP) { enq(p) })
	s.OnASS(func(p *aolib.ASS) { enq(p) })
	s.OnAUTH(func(p *aolib.AUTH) { enq(p) })
	s.OnBB(func(p *aolib.BB) { enq(p) })
	s.OnBD(func(p *aolib.BD) { enq(p) })
	s.OnBN(func(p *aolib.BN) { enq(p) })
	s.OnCHECK(func(p *aolib.CHECK) { enq(p) })
	s.OnCI(func(p *aolib.CI) { enq(p) })
	s.OnCT(func(p *aolib.CTToClient) { enq(p) })
	s.OnCharsCheck(func(p *aolib.CharsCheck) { enq(p) })
	s.OnDONE(func(p *aolib.DONE) { enq(p) })
	s.OnEI(func(p *aolib.EI) { enq(p) })
	s.OnEM(func(p *aolib.EM) { enq(p) })
	s.OnFA(func(p *aolib.FA) { enq(p) })
	s.OnFL(func(p *aolib.FL) { enq(p) })
	s.OnFM(func(p *aolib.FM) { enq(p) })
	s.OnHP(func(p *aolib.HPToClient) { enq(p) })
	s.OnID(func(p *aolib.IDToClient) { enq(p) })
	s.OnJD(func(p *aolib.JD) { enq(p) })
	s.OnKB(func(p *aolib.KB) { enq(p) })
	s.OnKK(func(p *aolib.KK) { enq(p) })
	s.OnLE(func(p *aolib.LE) { enq(p) })
	s.OnMC(func(p *aolib.MCToClient) { enq(p) })
	s.OnMS(func(p *aolib.MSToClient) { enq(p) })
	s.OnPN(func(p *aolib.PN) { enq(p) })
	s.OnPR(func(p *aolib.PR) { enq(p) })
	s.OnPU(func(p *aolib.PU) { enq(p) })
	s.OnPV(func(p *aolib.PV) { enq(p) })
	s.OnRMC(func(p *aolib.RMC) { enq(p) })
	s.OnRT(func(p *aolib.RTToClient) { enq(p) })
	s.OnSC(func(p *aolib.SC) { enq(p) })
	s.OnSI(func(p *aolib.SI) { enq(p) })
	s.OnSM(func(p *aolib.SM) { enq(p) })
	s.OnSP(func(p *aolib.SP) { enq(p) })
	s.OnTI(func(p *aolib.TI) { enq(p) })
	s.OnZZ(func(p *aolib.ZZToClient) { enq(p) })
	// decryptor also flips the outbound wire mode when the server advertises JSON.
	s.OnDecryptor(func(p *aolib.Decryptor) {
		if strings.EqualFold(p.Value, "json") {
			c.jsonMode.Store(true)
		}
		enq(p)
	})

	// VS_* custom headers (registered codecs).
	for _, h := range voiceHeaders {
		_ = s.OnCustom(h, func(p any) {
			if o, ok := p.(aolib.Outgoing); ok {
				c.enqueue(Packet{Header: o.Header(), Fields: unescapeArgs(o.Args()), typed: o})
			}
		})
	}

	// GP: the JSON-only group-pair roster announcement (Nyathena extension).
	_ = s.OnCustom("GP", func(p any) {
		if o, ok := p.(aolib.Outgoing); ok {
			c.enqueue(Packet{Header: o.Header(), Fields: unescapeArgs(o.Args()), typed: o})
		}
	})
}
