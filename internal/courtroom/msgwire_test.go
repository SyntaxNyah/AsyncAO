package courtroom

import "testing"

func TestMessageFrameRoundTrip(t *testing.T) {
	cases := []WireMessage{
		{Kind: MsgDM},
		{Kind: MsgGroupText, GroupID: 0xDEADBEEF},
		{Kind: MsgInvite, GroupID: 42, GroupName: "Defense Squad"},
		{Kind: MsgKick, GroupID: 7, TargetUID: 1234},
		{Kind: MsgLeave, GroupID: 9},
	}
	for _, want := range cases {
		body := "hello there" + want.EncodeMarker()
		got, clean, ok := DecodeMessageFrame(body)
		if !ok {
			t.Fatalf("%+v: no frame decoded", want)
		}
		if got != want {
			t.Errorf("round-trip = %+v, want %+v", got, want)
		}
		if clean != "hello there" {
			t.Errorf("clean text = %q, want the visible text only", clean)
		}
	}
}

func TestMessageFramePlainText(t *testing.T) {
	if _, _, ok := DecodeMessageFrame("just a normal PM"); ok {
		t.Error("plain text must not decode as a frame")
	}
}

func TestPMCommand(t *testing.T) {
	if got := PMCommand([]int{5}, "hi"); got != "/pm 5 hi" {
		t.Errorf("DM cmd = %q", got)
	}
	if got := PMCommand([]int{1, 2, 3}, "yo"); got != "/pm 1,2,3 yo" {
		t.Errorf("group cmd = %q", got)
	}
	if got := PMCommand(nil, "x"); got != "" {
		t.Errorf("empty uids = %q, want empty", got)
	}
}

func TestParsePMSender(t *testing.T) {
	uid, name, ok := ParsePMSender("[PM] [UID 5] Phoenix")
	if !ok || uid != 5 || name != "Phoenix" {
		t.Errorf("ParsePMSender = (%d, %q, %v)", uid, name, ok)
	}
	if _, _, ok := ParsePMSender("[PM → [3] Maya] Phoenix"); ok {
		t.Error("the sender's own echo must not parse as a received PM")
	}
	if _, _, ok := ParsePMSender("normal OOC line"); ok {
		t.Error("plain OOC must not parse as a PM")
	}
}
