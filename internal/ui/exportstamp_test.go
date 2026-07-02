package ui

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestExportStamp pins the #74 watermark resolution: off → "", custom text wins,
// blank text falls back to host · recorded-date, and a bundled archive's local
// origin (loopback) is never credited — the stamp degrades to the date alone.
func TestExportStamp(t *testing.T) {
	rec := &sceneRecording{Origin: "https://assets.example.net/base", RecordedAt: "2026-06-30T12:00:00Z"}

	if got := exportStamp(config.ExportOptions{}, rec); got != "" {
		t.Fatalf("watermark off must stamp nothing, got %q", got)
	}
	if got := exportStamp(config.ExportOptions{Watermark: true, WatermarkText: "  my case  "}, rec); got != "my case" {
		t.Fatalf("custom text must win (trimmed), got %q", got)
	}
	got := exportStamp(config.ExportOptions{Watermark: true}, rec)
	if got != "assets.example.net · 30 Jun 2026" {
		t.Fatalf("auto stamp = host · recorded date, got %q", got)
	}
	// Bundled replays rewrite Origin to a loopback base — never stamp that.
	rec.Origin = "http://127.0.0.1:39321/"
	got = exportStamp(config.ExportOptions{Watermark: true}, rec)
	if strings.Contains(got, "127.0.0.1") || got != "30 Jun 2026" {
		t.Fatalf("loopback origin must degrade to the date alone, got %q", got)
	}
	// An unparseable RecordedAt still stamps (today's date) rather than nothing.
	rec.RecordedAt = "garbage"
	if got = exportStamp(config.ExportOptions{Watermark: true}, rec); got == "" {
		t.Fatal("a bad RecordedAt must not kill the stamp")
	}
}
