// Package netdiscover advertises and discovers the Nimbus One HTTP gateway on
// the LAN via mDNS/DNS-SD (`_nimbus._tcp.local`) using only the Go standard
// library (no Cgo, no third-party deps).
//
// Known-failure fallbacks (log-and-continue, never crash the host app):
//
//  1. No multicast route (e.g. Termux hotspot / restricted Wi-Fi):
//     Advertise returns a descriptive error mentioning that
//     `nimbus-one serve` still works via direct IP. Runtime send errors after
//     the initial bind succeed are only logged via log/slog and the loop
//     continues.
//  2. Browse timeout: Browse returns an empty list and a nil error. It never
//     blocks longer than timeout+2s, and a missing multicast route falls back
//     to an ephemeral UDP socket (still returns empty + nil error).
package netdiscover

import (
	"context"
	"encoding/binary"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// mDNS multicast endpoints.
	mdnsIPv4Addr = "224.0.0.251:5353"
	mdnsIPv6Addr = "[ff02::fb]:5353"

	// Default DNS-SD identifiers for Nimbus One.
	defaultService = "_nimbus._tcp"
	defaultDomain  = "local"

	// DNS types we encode/parse.
	dnsTypeA    = 1
	dnsTypePTR  = 12
	dnsTypeTXT  = 16
	dnsTypeAAAA = 28
	dnsTypeSRV  = 33

	dnsClassIN = 1

	// mDNS response flags: QR + AA.
	mdnsResponseFlags = 0x8400

	// Announce behaviour.
	announceBurst    = 3
	announceGap      = 1 * time.Second
	announceInterval = 120 * time.Second

	defaultTTL = uint32(120)
)

// Service describes one DNS-SD instance.
type Service struct {
	Instance string
	Service  string
	Domain   string
	Host     string
	Port     int
	TXT      map[string]string
	IPs      []net.IP
}

// ---------------------------------------------------------------------------
// tiny error helper (avoids fmt/errors imports so the package only uses the
// allow-listed stdlib packages).
// ---------------------------------------------------------------------------

type stringError struct{ s string }

func (e *stringError) Error() string { return e.s }

func newError(msg string) error { return &stringError{s: msg} }

func wrapError(msg string, err error) error {
	if err == nil {
		return &stringError{s: msg}
	}
	return &stringError{s: msg + ": " + err.Error()}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// ---------------------------------------------------------------------------
// name normalisation
// ---------------------------------------------------------------------------

func ensureTrailingDot(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "."
	}
	if strings.HasSuffix(s, ".") {
		return s
	}
	return s + "."
}

func trimTrailingDot(s string) string {
	return strings.TrimSuffix(strings.TrimSpace(s), ".")
}

func serviceFQDN(svc Service) string {
	s := strings.TrimSpace(svc.Service)
	if s == "" {
		s = defaultService
	}
	d := strings.TrimSpace(svc.Domain)
	if d == "" {
		d = defaultDomain
	}
	s = trimTrailingDot(s)
	d = trimTrailingDot(d)
	if s == "" {
		s = defaultService
	}
	if d == "" {
		d = defaultDomain
	}
	return ensureTrailingDot(s + "." + d)
}

func instanceFQDN(svc Service) string {
	inst := strings.TrimSpace(svc.Instance)
	if inst == "" {
		inst = defaultInstance()
	}
	// If the caller already passed a FQDN, keep it.
	lower := strings.ToLower(trimTrailingDot(inst))
	svcFQDN := trimTrailingDot(serviceFQDN(svc))
	if strings.HasSuffix(strings.ToLower(inst), strings.ToLower(svcFQDN)) || strings.Contains(lower, "._tcp.") {
		return ensureTrailingDot(inst)
	}
	return ensureTrailingDot(inst + "." + trimTrailingDot(serviceFQDN(svc)))
}

func hostFQDN(svc Service) string {
	h := strings.TrimSpace(svc.Host)
	if h == "" {
		h = defaultHost()
	}
	if !strings.Contains(trimTrailingDot(h), ".") {
		h = trimTrailingDot(h) + ".local"
	}
	return ensureTrailingDot(h)
}

func defaultHost() string {
	if h, err := os.Hostname(); err == nil && strings.TrimSpace(h) != "" {
		h = strings.TrimSpace(h)
		// Strip any domain the OS reports; we re-append .local below if bare.
		if i := strings.Index(h, "."); i >= 0 {
			// Keep full hostname if it already looks like FQDN, else bare label.
			// Simplest: keep bare label to avoid leaking search domains.
			h = h[:i]
			if h == "" {
				return "nimbus-one"
			}
		}
		return h
	}
	return "nimbus-one"
}

func defaultInstance() string {
	return defaultHost()
}

// ---------------------------------------------------------------------------
// DNS name encode/decode (encode simple, no compression; decode handles
// RFC 1035 compression pointers).
// ---------------------------------------------------------------------------

// EncodeName encodes a DNS name without compression.
func EncodeName(name string) []byte {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, ".")
	if name == "" || name == "." {
		return []byte{0}
	}
	labels := strings.Split(name, ".")
	out := make([]byte, 0, len(name)+2)
	for _, l := range labels {
		if len(l) == 0 {
			continue
		}
		if len(l) > 63 {
			l = l[:63]
		}
		out = append(out, byte(len(l)))
		out = append(out, []byte(l)...)
	}
	out = append(out, 0)
	return out
}

// DecodeName decodes a (possibly compressed) DNS name at offset off.
// Returns the FQDN with trailing dot and the offset of the next field.
func DecodeName(buf []byte, off int) (string, int, error) {
	if off < 0 || off >= len(buf) {
		return "", off, newError("netdiscover: decode name offset out of range")
	}
	var parts []string
	jumped := false
	newOff := off
	jumps := 0
	for {
		if off >= len(buf) {
			return "", newOff, newError("netdiscover: truncated DNS name")
		}
		b := buf[off]
		if b&0xC0 == 0xC0 {
			if off+1 >= len(buf) {
				return "", newOff, newError("netdiscover: truncated compression pointer")
			}
			ptr := int(b&0x3F)<<8 | int(buf[off+1])
			if ptr >= len(buf) {
				return "", newOff, newError("netdiscover: bad compression pointer")
			}
			jumps++
			if jumps > 10 {
				return "", newOff, newError("netdiscover: compression pointer loop")
			}
			if !jumped {
				newOff = off + 2
			}
			jumped = true
			off = ptr
			continue
		}
		if b == 0 {
			if !jumped {
				newOff = off + 1
			}
			break
		}
		length := int(b)
		off++
		if length == 0 {
			if !jumped {
				newOff = off
			}
			break
		}
		if off+length > len(buf) {
			return "", newOff, newError("netdiscover: truncated DNS label")
		}
		parts = append(parts, string(buf[off:off+length]))
		off += length
		if !jumped {
			newOff = off
		}
	}
	if len(parts) == 0 {
		return ".", newOff, nil
	}
	return strings.Join(parts, ".") + ".", newOff, nil
}

// Lowercase aliases for test convenience (same package tests may use either).
func encodeName(name string) []byte { return EncodeName(name) }

func decodeName(buf []byte, off int) (string, int, error) { return DecodeName(buf, off) }

// ---------------------------------------------------------------------------
// Announce packet building.
// ---------------------------------------------------------------------------

func encodeTXT(txt map[string]string) []byte {
	if len(txt) == 0 {
		return []byte{0}
	}
	out := make([]byte, 0, 64)
	for k, v := range txt {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		entry := k
		if v != "" {
			entry = k + "=" + v
		}
		if len(entry) > 255 {
			entry = entry[:255]
		}
		out = append(out, byte(len(entry)))
		out = append(out, []byte(entry)...)
	}
	if len(out) == 0 {
		return []byte{0}
	}
	return out
}

func parseTXTRData(rdata []byte) map[string]string {
	out := make(map[string]string)
	off := 0
	for off < len(rdata) {
		l := int(rdata[off])
		off++
		if l == 0 {
			continue
		}
		if off+l > len(rdata) {
			break
		}
		entry := string(rdata[off : off+l])
		off += l
		if idx := strings.Index(entry, "="); idx < 0 {
			out[entry] = ""
		} else {
			out[entry[:idx]] = entry[idx+1:]
		}
	}
	return out
}

func appendRR(packet []byte, name []byte, typ, class uint16, ttl uint32, rdata []byte) []byte {
	packet = append(packet, name...)
	var tmp [4]byte
	binary.BigEndian.PutUint16(tmp[:2], typ)
	packet = append(packet, tmp[:2]...)
	binary.BigEndian.PutUint16(tmp[:2], class)
	packet = append(packet, tmp[:2]...)
	binary.BigEndian.PutUint32(tmp[:4], ttl)
	packet = append(packet, tmp[:4]...)
	binary.BigEndian.PutUint16(tmp[:2], uint16(len(rdata)))
	packet = append(packet, tmp[:2]...)
	packet = append(packet, rdata...)
	return packet
}

// BuildAnnounce builds an mDNS response packet advertising svc.
func BuildAnnounce(svc Service) ([]byte, error) {
	if strings.TrimSpace(svc.Service) == "" {
		svc.Service = defaultService
	}
	if strings.TrimSpace(svc.Domain) == "" {
		svc.Domain = defaultDomain
	}
	if strings.TrimSpace(svc.Instance) == "" {
		svc.Instance = defaultInstance()
	}
	if strings.TrimSpace(svc.Host) == "" {
		svc.Host = defaultHost()
	}
	if svc.Port <= 0 || svc.Port > 65535 {
		return nil, newError("netdiscover: invalid service port " + itoa(svc.Port) + " (must be 1-65535)")
	}
	ips := svc.IPs
	if len(ips) == 0 {
		got, _ := LocalIPs()
		ips = got
	}
	// Filter invalid IPs.
	valid := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		if ip.To4() != nil || ip.To16() != nil {
			valid = append(valid, ip)
		}
	}
	if len(valid) == 0 {
		valid = []net.IP{net.ParseIP("127.0.0.1")}
	}

	svcFQDN := serviceFQDN(svc)
	instFQDN := instanceFQDN(svc)
	hFQDN := hostFQDN(svc)

	anCount := 3 + len(valid) // PTR + SRV + TXT + A/AAAA...

	packet := make([]byte, 0, 512)
	var hdr [12]byte
	// ID=0 for mDNS, flags=response+authoritative.
	binary.BigEndian.PutUint16(hdr[0:2], 0)
	binary.BigEndian.PutUint16(hdr[2:4], mdnsResponseFlags)
	binary.BigEndian.PutUint16(hdr[4:6], 0) // QDCOUNT
	binary.BigEndian.PutUint16(hdr[6:8], uint16(anCount))
	binary.BigEndian.PutUint16(hdr[8:10], 0)
	binary.BigEndian.PutUint16(hdr[10:12], 0)
	packet = append(packet, hdr[:]...)

	// PTR: service -> instance.
	packet = appendRR(packet, EncodeName(svcFQDN), dnsTypePTR, dnsClassIN, defaultTTL, EncodeName(instFQDN))

	// SRV: instance -> host:port.
	srvRData := make([]byte, 0, 8+len(hFQDN)+2)
	var tmp [6]byte
	binary.BigEndian.PutUint16(tmp[0:2], 0) // priority
	binary.BigEndian.PutUint16(tmp[2:4], 0) // weight
	binary.BigEndian.PutUint16(tmp[4:6], uint16(svc.Port))
	srvRData = append(srvRData, tmp[:]...)
	srvRData = append(srvRData, EncodeName(hFQDN)...)
	packet = appendRR(packet, EncodeName(instFQDN), dnsTypeSRV, 0x8001, defaultTTL, srvRData)

	// TXT: instance -> key=value strings.
	packet = appendRR(packet, EncodeName(instFQDN), dnsTypeTXT, 0x8001, defaultTTL, encodeTXT(svc.TXT))

	// A / AAAA per IP.
	for _, ip := range valid {
		if v4 := ip.To4(); v4 != nil {
			rdata := make([]byte, 4)
			copy(rdata, v4)
			packet = appendRR(packet, EncodeName(hFQDN), dnsTypeA, 0x8001, defaultTTL, rdata)
		} else if v6 := ip.To16(); v6 != nil {
			rdata := make([]byte, 16)
			copy(rdata, v6)
			packet = appendRR(packet, EncodeName(hFQDN), dnsTypeAAAA, 0x8001, defaultTTL, rdata)
		}
	}
	return packet, nil
}

func buildAnnounce(svc Service) ([]byte, error) { return BuildAnnounce(svc) }

// BuildQuery builds an mDNS PTR query for the given service FQDN.
func BuildQuery(serviceFQDN string) []byte {
	if strings.TrimSpace(serviceFQDN) == "" {
		serviceFQDN = defaultService + "." + defaultDomain + "."
	}
	serviceFQDN = ensureTrailingDot(serviceFQDN)
	packet := make([]byte, 0, 64)
	var hdr [12]byte
	binary.BigEndian.PutUint16(hdr[0:2], 0)
	binary.BigEndian.PutUint16(hdr[2:4], 0) // query flags
	binary.BigEndian.PutUint16(hdr[4:6], 1) // QDCOUNT
	packet = append(packet, hdr[:]...)
	packet = append(packet, EncodeName(serviceFQDN)...)
	var tmp [4]byte
	binary.BigEndian.PutUint16(tmp[0:2], dnsTypePTR)
	packet = append(packet, tmp[0:2]...)
	binary.BigEndian.PutUint16(tmp[0:2], dnsClassIN)
	packet = append(packet, tmp[0:2]...)
	return packet
}

func buildQuery(serviceFQDN string) []byte { return BuildQuery(serviceFQDN) }

// ---------------------------------------------------------------------------
// Packet parsing.
// ---------------------------------------------------------------------------

type dnsRR struct {
	name     string
	typ      uint16
	ttl      uint32
	ptr      string
	srvHost  string
	srvPort  uint16
	txt      map[string]string
	ip       net.IP
	rdLength int
}

func splitServiceFQDN(fqdn string) (service, domain string) {
	trimmed := trimTrailingDot(fqdn)
	parts := strings.Split(trimmed, ".")
	if len(parts) >= 3 {
		service = parts[0] + "." + parts[1]
		domain = strings.Join(parts[2:], ".")
		return service, domain
	}
	return defaultService, defaultDomain
}

func instanceShort(instFQDN, svcFQDN string) string {
	inst := trimTrailingDot(instFQDN)
	svc := trimTrailingDot(svcFQDN)
	if strings.EqualFold(inst, svc) {
		return inst
	}
	suffix := "." + svc
	if len(inst) > len(suffix) && strings.HasSuffix(strings.ToLower(inst), strings.ToLower(suffix)) {
		return inst[:len(inst)-len(suffix)]
	}
	// Fallback: strip a trailing service-like suffix if present.
	if idx := strings.Index(strings.ToLower(inst), "._tcp."); idx >= 0 {
		return inst[:idx]
	}
	return inst
}

// ParsePacket parses an mDNS response/query packet and reconstructs the
// advertised services from its PTR/SRV/TXT/A/AAAA records.
func ParsePacket(buf []byte) ([]Service, error) {
	if len(buf) < 12 {
		return nil, newError("netdiscover: packet too short")
	}
	qdCount := int(binary.BigEndian.Uint16(buf[4:6]))
	anCount := int(binary.BigEndian.Uint16(buf[6:8]))
	nsCount := int(binary.BigEndian.Uint16(buf[8:10]))
	arCount := int(binary.BigEndian.Uint16(buf[10:12]))

	off := 12
	// Skip questions.
	for i := 0; i < qdCount; i++ {
		_, next, err := DecodeName(buf, off)
		if err != nil {
			return nil, err
		}
		off = next
		if off+4 > len(buf) {
			return nil, newError("netdiscover: truncated question")
		}
		off += 4
	}

	totalRR := anCount + nsCount + arCount
	rrs := make([]dnsRR, 0, totalRR)
	for i := 0; i < totalRR; i++ {
		if off >= len(buf) {
			break
		}
		name, next, err := DecodeName(buf, off)
		if err != nil {
			return nil, err
		}
		off = next
		if off+10 > len(buf) {
			return nil, newError("netdiscover: truncated RR header")
		}
		typ := binary.BigEndian.Uint16(buf[off : off+2])
		// Mask off the mDNS cache-flush bit (0x8000) to get the class.
		_ = binary.BigEndian.Uint16(buf[off+2 : off+4])
		ttl := binary.BigEndian.Uint32(buf[off+4 : off+8])
		rdLen := int(binary.BigEndian.Uint16(buf[off+8 : off+10]))
		off += 10
		if off+rdLen > len(buf) {
			return nil, newError("netdiscover: truncated RDATA")
		}
		rdataOff := off
		rdata := buf[off : off+rdLen]
		off += rdLen

		rr := dnsRR{name: name, typ: typ, ttl: ttl, rdLength: rdLen}
		switch typ {
		case dnsTypePTR:
			target, _, err := DecodeName(buf, rdataOff)
			if err != nil {
				continue
			}
			rr.ptr = target
		case dnsTypeSRV:
			if rdLen < 6 {
				continue
			}
			rr.srvPort = binary.BigEndian.Uint16(rdata[4:6])
			target, _, err := DecodeName(buf, rdataOff+6)
			if err != nil {
				continue
			}
			rr.srvHost = target
		case dnsTypeTXT:
			rr.txt = parseTXTRData(rdata)
		case dnsTypeA:
			if rdLen < 4 {
				continue
			}
			ip := make(net.IP, 4)
			copy(ip, rdata[:4])
			rr.ip = ip
		case dnsTypeAAAA:
			if rdLen < 16 {
				continue
			}
			ip := make(net.IP, 16)
			copy(ip, rdata[:16])
			rr.ip = ip
		default:
			continue
		}
		rrs = append(rrs, rr)
	}

	// Aggregate: PTR -> instances; SRV/TXT keyed by instance; A/AAAA keyed by host.
	type instInfo struct {
		serviceFQDN string
		host        string
		port        int
		txt         map[string]string
		ips         []net.IP
	}
	byInst := make(map[string]*instInfo)
	hostToIPs := make(map[string][]net.IP)

	for _, rr := range rrs {
		switch rr.typ {
		case dnsTypeA, dnsTypeAAAA:
			if rr.ip == nil {
				continue
			}
			key := strings.ToLower(trimTrailingDot(rr.name))
			hostToIPs[key] = append(hostToIPs[key], rr.ip)
		}
	}
	for _, rr := range rrs {
		switch rr.typ {
		case dnsTypePTR:
			if rr.ptr == "" {
				continue
			}
			key := strings.ToLower(trimTrailingDot(rr.ptr))
			info, ok := byInst[key]
			if !ok {
				info = &instInfo{serviceFQDN: trimTrailingDot(rr.name)}
				byInst[key] = info
			} else if info.serviceFQDN == "" {
				info.serviceFQDN = trimTrailingDot(rr.name)
			}
		case dnsTypeSRV:
			key := strings.ToLower(trimTrailingDot(rr.name))
			info, ok := byInst[key]
			if !ok {
				info = &instInfo{}
				byInst[key] = info
			}
			info.host = trimTrailingDot(rr.srvHost)
			info.port = int(rr.srvPort)
		case dnsTypeTXT:
			key := strings.ToLower(trimTrailingDot(rr.name))
			info, ok := byInst[key]
			if !ok {
				info = &instInfo{}
				byInst[key] = info
			}
			if info.txt == nil {
				info.txt = make(map[string]string)
			}
			for k, v := range rr.txt {
				info.txt[k] = v
			}
		}
	}

	out := make([]Service, 0, len(byInst))
	for instKey, info := range byInst {
		_ = instKey
		// Resolve instance FQDN display: byInst keys are trimmed; rebuild.
		svcName, domain := splitServiceFQDN(ensureTrailingDot(info.serviceFQDN))
		if info.serviceFQDN == "" {
			svcName, domain = defaultService, defaultDomain
		}
		svcFQDN := ensureTrailingDot(svcName + "." + domain)
		instFQDNStr := ensureTrailingDot(strings.TrimSpace(instKey) + "." + trimTrailingDot(svcFQDN))
		// Recover original-case instance short name from key (lowercased).
		// Keys are lowercased; use the stored PTR target case when available by
		// scanning RRs again for a case-preserving match.
		short := instanceShort(instFQDNStr, svcFQDN)
		for _, rr := range rrs {
			if rr.typ == dnsTypePTR && strings.EqualFold(trimTrailingDot(rr.ptr), instKey) {
				short = instanceShort(trimTrailingDot(rr.ptr)+".", svcFQDN)
				// Prefer the PTR target up to service suffix with original case.
				// instanceShort already handles suffix stripping.
				break
			}
		}
		var ips []net.IP
		if info.host != "" {
			ips = append(ips, hostToIPs[strings.ToLower(info.host)]...)
		}
		out = append(out, Service{
			Instance: short,
			Service:  svcName,
			Domain:   domain,
			Host:     info.host,
			Port:     info.port,
			TXT:      info.txt,
			IPs:      ips,
		})
	}
	return out, nil
}

func parsePacket(buf []byte) ([]Service, error) { return ParsePacket(buf) }

// ---------------------------------------------------------------------------
// LocalIPs
// ---------------------------------------------------------------------------

// LocalIPs returns non-loopback IPv4 addresses, falling back to 127.0.0.1
// when none are available (e.g. isolated hotspot / airplane mode).
func LocalIPs() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return []net.IP{net.ParseIP("127.0.0.1")}, wrapError("netdiscover: cannot list interfaces", err)
	}
	var out []net.IP
	for _, iface := range ifaces {
		addrs, aerr := iface.Addrs()
		if aerr != nil {
			slog.Debug("netdiscover: cannot read interface addrs", "iface", iface.Name, "err", aerr)
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				cp := make(net.IP, 4)
				copy(cp, v4)
				out = append(out, cp)
			}
		}
	}
	if len(out) == 0 {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Advertise
// ---------------------------------------------------------------------------

// Advertise announces svc on the LAN via mDNS until ctx is done.
//
// It joins 224.0.0.251:5353 (IPv4) and best-effort [ff02::fb]:5353 (IPv6,
// errors ignored). It sends 3 announcements 1s apart, then re-announces
// every 120s. Only an initial bind failure is returned as an error (with a
// note that `nimbus-one serve` still works via direct IP); all later socket
// errors are logged via log/slog and the loop continues so the host app
// never crashes because of discovery.
func Advertise(ctx context.Context, svc Service) error {
	if ctx == nil {
		return newError("netdiscover: nil context")
	}
	if strings.TrimSpace(svc.Service) == "" {
		svc.Service = defaultService
	}
	if strings.TrimSpace(svc.Domain) == "" {
		svc.Domain = defaultDomain
	}
	if strings.TrimSpace(svc.Instance) == "" {
		svc.Instance = defaultInstance()
	}
	if strings.TrimSpace(svc.Host) == "" {
		svc.Host = defaultHost()
	}
	if svc.Port <= 0 || svc.Port > 65535 {
		return newError("netdiscover: invalid port " + itoa(svc.Port) + " (must be 1-65535); nimbus-one serve still works via direct IP")
	}
	if len(svc.IPs) == 0 {
		ips, _ := LocalIPs()
		svc.IPs = ips
	}

	packet, err := BuildAnnounce(svc)
	if err != nil {
		return err
	}

	v4Dst, err := net.ResolveUDPAddr("udp4", mdnsIPv4Addr)
	if err != nil {
		return wrapError("netdiscover: cannot resolve mDNS IPv4 group; nimbus-one serve still works via direct IP", err)
	}

	// Join-check: prove multicast is usable. The socket is closed immediately;
	// the real sending socket is created below. Failure here means no
	// multicast route (common on Termux hotspots).
	if jc, jerr := net.ListenMulticastUDP("udp4", nil, v4Dst); jerr != nil {
		return wrapError("netdiscover: no multicast route (mDNS unavailable); nimbus-one serve still works via direct IP", jerr)
	} else {
		_ = jc.Close()
	}

	v4Conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return wrapError("netdiscover: mDNS initial bind failed (no multicast route?); nimbus-one serve still works via direct IP", err)
	}
	defer func() { _ = v4Conn.Close() }()

	// Best-effort IPv6 sender; ignore all errors per spec.
	var v6Conn net.PacketConn
	v6Dst, verr := net.ResolveUDPAddr("udp6", mdnsIPv6Addr)
	if verr == nil {
		if c6, cerr := net.ListenPacket("udp6", "[::]:0"); cerr != nil {
			slog.Debug("netdiscover: IPv6 mDNS unavailable (ignored)", "err", cerr)
		} else {
			v6Conn = c6
			defer func() { _ = v6Conn.Close() }()
		}
	} else {
		slog.Debug("netdiscover: IPv6 mDNS resolve failed (ignored)", "err", verr)
	}

	send := func() {
		if _, serr := v4Conn.WriteTo(packet, v4Dst); serr != nil {
			slog.Warn("netdiscover: mDNS announce send failed (continuing)", "err", serr)
		} else {
			slog.Debug("netdiscover: mDNS announce sent", "instance", svc.Instance, "port", svc.Port)
		}
		if v6Conn != nil && v6Dst != nil {
			if _, serr := v6Conn.WriteTo(packet, v6Dst); serr != nil {
				slog.Debug("netdiscover: IPv6 announce failed (ignored)", "err", serr)
			}
		}
	}

	for i := 0; i < announceBurst; i++ {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		send()
		if i < announceBurst-1 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(announceGap):
			}
		}
	}

	ticker := time.NewTicker(announceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			send()
		}
	}
}

// ---------------------------------------------------------------------------
// Browse
// ---------------------------------------------------------------------------

// Browse sends a PTR query for _nimbus._tcp.local and collects responses
// until timeout. On timeout it returns whatever was found (possibly empty)
// with a nil error. It never blocks longer than timeout+2s and never crashes
// the caller: bind/send failures are logged and yield empty + nil.
func Browse(ctx context.Context, timeout time.Duration) ([]Service, error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if ctx == nil {
		return []Service{}, newError("netdiscover: nil context")
	}

	mdnsAddr, rerr := net.ResolveUDPAddr("udp4", mdnsIPv4Addr)
	if rerr != nil {
		slog.Warn("netdiscover: cannot resolve mDNS group, returning empty", "err", rerr)
		return []Service{}, nil
	}

	var conn net.PacketConn
	if mc, merr := net.ListenMulticastUDP("udp4", nil, mdnsAddr); merr == nil {
		conn = mc
	} else {
		slog.Debug("netdiscover: multicast listen unavailable, using ephemeral port", "err", merr)
		ec, eerr := net.ListenPacket("udp4", "0.0.0.0:0")
		if eerr != nil {
			slog.Warn("netdiscover: Browse bind failed, returning empty", "err", eerr)
			return []Service{}, nil
		}
		conn = ec
	}
	defer func() { _ = conn.Close() }()

	query := BuildQuery(defaultService + "." + defaultDomain + ".")
	if _, serr := conn.WriteTo(query, mdnsAddr); serr != nil {
		slog.Warn("netdiscover: Browse query send failed (still listening)", "err", serr)
	}

	deadline := time.Now().Add(timeout)
	hardDeadline := time.Now().Add(timeout + 2*time.Second)

	byInst := make(map[string]*Service)
	var mu sync.Mutex

	// Short read chunks so ctx cancellation is honoured promptly.
	for {
		if time.Now().After(hardDeadline) {
			break
		}
		select {
		case <-ctx.Done():
			mu.Lock()
			out := collectServices(byInst)
			mu.Unlock()
			return out, nil
		default:
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		chunk := 200 * time.Millisecond
		if remaining < chunk {
			chunk = remaining
		}
		_ = conn.SetReadDeadline(time.Now().Add(chunk))
		buf := make([]byte, 9000)
		n, _, rerr := conn.ReadFrom(buf)
		if rerr != nil {
			// Timeout chunks are expected; only stop at the real deadline.
			if time.Now().After(deadline) {
				break
			}
			continue
		}
		if n < 12 {
			continue
		}
		parsed, perr := ParsePacket(buf[:n])
		if perr != nil {
			slog.Debug("netdiscover: ignoring malformed mDNS packet", "err", perr)
			continue
		}
		mu.Lock()
		for _, s := range parsed {
			key := strings.ToLower(strings.TrimSpace(s.Instance)) + "|" + itoa(s.Port)
			if key == "|" || strings.TrimSpace(s.Instance) == "" {
				continue
			}
			existing, ok := byInst[key]
			if !ok {
				cp := s
				cp.TXT = cloneTXT(s.TXT)
				cp.IPs = cloneIPs(s.IPs)
				byInst[key] = &cp
				continue
			}
			for _, ip := range s.IPs {
				dup := false
				for _, have := range existing.IPs {
					if have.Equal(ip) {
						dup = true
						break
					}
				}
				if !dup {
					existing.IPs = append(existing.IPs, ip)
				}
			}
			if len(existing.TXT) == 0 && len(s.TXT) > 0 {
				existing.TXT = cloneTXT(s.TXT)
			}
			if existing.Host == "" && s.Host != "" {
				existing.Host = s.Host
			}
		}
		mu.Unlock()
	}

	mu.Lock()
	out := collectServices(byInst)
	mu.Unlock()
	return out, nil
}

func collectServices(m map[string]*Service) []Service {
	out := make([]Service, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	return out
}

func cloneTXT(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneIPs(in []net.IP) []net.IP {
	if in == nil {
		return nil
	}
	out := make([]net.IP, 0, len(in))
	for _, ip := range in {
		cp := make(net.IP, len(ip))
		copy(cp, ip)
		out = append(out, cp)
	}
	return out
}
