package toast

import (
	"encoding/xml"
	"testing"
)

// textElements is the title and the body.
const textElements = 2

// A crafted showname must stay inside its text element, whatever markup it carries.
func TestToastXMLKeepsTextInsideItsElement(t *testing.T) {
	for _, text := range []string{
		"plain name",
		`a&<b>"c'`,
		`</text></binding><binding template="x"><text>`,
		"José ゲーム \U0001F600",
		"line\nbreak\tend",
	} {
		var doc struct {
			Texts []string `xml:"visual>binding>text"`
		}
		if err := xml.Unmarshal([]byte(toastXML(text, text)), &doc); err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		if len(doc.Texts) != textElements || doc.Texts[0] != text || doc.Texts[1] != text {
			t.Errorf("%q came back as %q", text, doc.Texts)
		}
	}
}

func TestToastXMLStaysWellFormedWithControlCharacters(t *testing.T) {
	var doc struct {
		Texts []string `xml:"visual>binding>text"`
	}
	if err := xml.Unmarshal([]byte(toastXML("bell\x07", "nul\x00")), &doc); err != nil {
		t.Fatal(err)
	}
}
