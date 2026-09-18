package netdiscover

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestLocalIPsNonEmpty(t *testing.T) {
	ips, err := LocalIPs()
	if err != nil {
		// LocalIPs may return a non-nil error only when interfaces cannot be
		// listed; it must still return the 127.0.0.1 fallback.
		t.Logf("LocalIPs returned error (fallback expected): %v", err)
	}
	if len(ips) < 1 {
		t.Fatalf("LocalIPs returned no addresses, want >= 1")
	}
	for _, ip := range ips {
		if ip == nil {
			t.Fatalf("LocalIPs returned nil IP")
		}
	}
}

func TestBrowseTerminates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	svcs, err := Browse(ctx, 300*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Browse returned error: %v", err)
	}
	if svcs == nil {
		t.Fatalf("Browse returned nil slice, want non-nil (possibly empty)")
	}
	// Hermetic: may be empty on hosts without peers; only assert no hang.
	// Must never exceed timeout+2s (300ms+2s); give 1s scheduling slack.
	if elapsed > 3300*time.Millisecond {
		t.Fatalf("Browse exceeded timeout+2s: elapsed=%v", elapsed)
	}
	t.Logf("Browse returned %d service(s) in %v", len(svcs), elapsed)
}

func TestEncodeDecodeNameRoundtrip(t *testing.T) {
	for _, name := range []string{"nimbus-one.local.", "_nimbus._tcp.local.", "My-Host.local.", "x.y.z."} {
		enc := EncodeName(name)
		if len(enc) == 0 {
			t.Fatalf("EncodeName(%q) returned empty", name)
		}
		dec, next, err := DecodeName(enc, 0)
		if err != nil {
			t.Fatalf("DecodeName(%q) error: %v", name, err)
		}
		if next != len(enc) {
			t.Fatalf("DecodeName(%q) next=%d, want %d", name, next, len(enc))
		}
		if !strings.EqualFold(dec, ensureTrailingDot(name)) {
			t.Fatalf("roundtrip mismatch: got %q want %q", dec, name)
		}
		// Lowercase alias must behave identically.
		enc2 := encodeName(name)
		if string(enc2) != string(enc) {
			t.Fatalf("encodeName alias mismatch for %q", name)
		}
		if _, _, err := decodeName(enc, 0); err != nil {
			t.Fatalf("decodeName alias error: %v", err)
		}
	}
}

func TestDecodeNameCompressionPointer(t *testing.T) {
	first := EncodeName("myservice.local.")
	// Append a pointer back to offset 0.
	buf := append(append([]byte{}, first...), 0xC0, 0x00)
	dec, next, err := DecodeName(buf, len(first))
	if err != nil {
		t.Fatalf("DecodeName pointer error: %v", err)
	}
	if !strings.EqualFold(dec, "myservice.local.") {
		t.Fatalf("pointer decode got %q", dec)
	}
	if next != len(buf) {
		t.Fatalf("pointer next=%d want %d", next, len(buf))
	}
}

func TestBuildAnnounceParsePacketRoundtrip(t *testing.T) {
	svc := Service{
		Instance: "TestNimbus-123",
		Service:  "_nimbus._tcp",
		Domain:   "local",
		Host:     "testhost.local",
		Port:     8080,
		TXT:      map[string]string{"model": "test", "ver": "1", "empty": ""},
		IPs:      []net.IP{net.ParseIP("192.168.1.10")},
	}
	pkt, err := BuildAnnounce(svc)
	if err != nil {
		t.Fatalf("BuildAnnounce error: %v", err)
	}
	if len(pkt) < 12 {
		t.Fatalf("packet too short: %d", len(pkt))
	}
	// Lowercase alias parity.
	pkt2, err := buildAnnounce(svc)
	if err != nil {
		t.Fatalf("buildAnnounce alias error: %v", err)
	}
	if len(pkt2) != len(pkt) {
		t.Fatalf("buildAnnounce alias length mismatch")
	}

	parsed, err := ParsePacket(pkt)
	if err != nil {
		t.Fatalf("ParsePacket error: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("ParsePacket returned %d services, want 1", len(parsed))
	}
	got := parsed[0]
	if !strings.EqualFold(got.Instance, svc.Instance) {
		t.Fatalf("instance mismatch: got %q want %q", got.Instance, svc.Instance)
	}
	if got.Service != "_nimbus._tcp" || got.Domain != "local" {
		t.Fatalf("service/domain mismatch: got %q/%q", got.Service, got.Domain)
	}
	if !strings.EqualFold(got.Host, "testhost.local") {
		t.Fatalf("host mismatch: got %q", got.Host)
	}
	if got.Port != 8080 {
		t.Fatalf("port mismatch: got %d", got.Port)
	}
	for k, v := range svc.TXT {
		if gv, ok := got.TXT[k]; !ok || gv != v {
			t.Fatalf("TXT mismatch for %q: got %q want %q", k, gv, v)
		}
	}
	if len(got.IPs) != 1 || !got.IPs[0].Equal(net.ParseIP("192.168.1.10")) {
		t.Fatalf("IPs mismatch: got %v", got.IPs)
	}

	// parsePacket alias parity.
	parsed2, err := parsePacket(pkt)
	if err != nil || len(parsed2) != 1 {
		t.Fatalf("parsePacket alias failed: %v %d", err, len(parsed2))
	}
}

func TestBuildQueryParseSkipsQuestions(t *testing.T) {
	q := BuildQuery("_nimbus._tcp.local.")
	if len(q) < 12 {
		t.Fatalf("query too short")
	}
	// A query has no answers; ParsePacket should return zero services, no error.
	svcs, err := ParsePacket(q)
	if err != nil {
		t.Fatalf("ParsePacket(query) error: %v", err)
	}
	if len(svcs) != 0 {
		t.Fatalf("ParsePacket(query) returned %d services, want 0", len(svcs))
	}
}
