package ssrf_test

import (
	"net"
	"testing"

	"github.com/zubimendi/fog-horn/internal/ssrf"
)

type testCase struct {
	name    string
	ip      string
	want    ssrf.Class
}

func TestClassify(t *testing.T) {
	cases := []testCase{
		// --- BLOCKED ---
		// Loopback
		{"loopback v4",          "127.0.0.1",        ssrf.Blocked},
		{"loopback v4 range",    "127.255.255.255",   ssrf.Blocked},
		{"loopback v6",          "::1",               ssrf.Blocked},
		// Unspecified
		{"unspecified v4",       "0.0.0.0",           ssrf.Blocked},
		// Link-local (cloud metadata)
		{"link-local meta",      "169.254.169.254",   ssrf.Blocked},
		{"link-local v4",        "169.254.0.1",       ssrf.Blocked},
		{"link-local v6",        "fe80::1",           ssrf.Blocked},
		// RFC 1918
		{"rfc1918 10/8",         "10.0.0.1",          ssrf.Blocked},
		{"rfc1918 172.16/12",    "172.16.0.1",        ssrf.Blocked},
		{"rfc1918 172.31.x.x",   "172.31.255.255",    ssrf.Blocked},
		{"rfc1918 192.168/16",   "192.168.1.1",       ssrf.Blocked},
		// CGNAT
		{"cgnat",                "100.64.0.1",        ssrf.Blocked},
		{"cgnat upper",          "100.127.255.255",   ssrf.Blocked},
		// IPv6 ULA
		{"ula fc",               "fc00::1",           ssrf.Blocked},
		{"ula fd",               "fd00::1",           ssrf.Blocked},
		// Multicast
		{"multicast v4",         "224.0.0.1",         ssrf.Blocked},
		{"multicast v6",         "ff02::1",           ssrf.Blocked},
		// IPv4-mapped IPv6 (key test)
		{"mapped loopback",      "::ffff:127.0.0.1",  ssrf.Blocked},
		{"mapped meta",          "::ffff:169.254.169.254", ssrf.Blocked},
		{"mapped rfc1918",       "::ffff:10.0.0.1",   ssrf.Blocked},
		// Documentation
		{"doc 192.0.2.x",        "192.0.2.1",         ssrf.Blocked},
		{"doc 198.51.100.x",     "198.51.100.1",      ssrf.Blocked},
		{"doc 203.0.113.x",      "203.0.113.1",       ssrf.Blocked},

		// --- ALLOWED ---
		{"public ipv4",          "8.8.8.8",           ssrf.Allowed},
		{"cloudflare",           "1.1.1.1",           ssrf.Allowed},
		{"public ipv6",          "2606:4700:4700::1111", ssrf.Allowed},
		{"public ipv4 b",        "93.184.216.34",     ssrf.Allowed}, // example.com
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("could not parse IP %q", tc.ip)
			}
			got := ssrf.Classify(ip)
			if got != tc.want {
				t.Errorf("Classify(%q) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestParseDecimalIP(t *testing.T) {
	cases := []struct {
		input string
		wantIP string
		wantNil bool
	}{
		{"2130706433", "127.0.0.1", false},  // 0x7f000001
		{"3232235520", "192.168.0.0", false}, // 0xC0A80000
		{"0", "0.0.0.0", false},
		{"4294967295", "255.255.255.255", false},
		{"4294967296", "", true},  // overflow
		{"not-a-number", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		ip := ssrf.ParseDecimalIP(tc.input)
		if tc.wantNil {
			if ip != nil {
				t.Errorf("ParseDecimalIP(%q) = %v, want nil", tc.input, ip)
			}
			continue
		}
		if ip == nil {
			t.Errorf("ParseDecimalIP(%q) = nil, want %v", tc.input, tc.wantIP)
			continue
		}
		if ip.String() != tc.wantIP {
			t.Errorf("ParseDecimalIP(%q) = %v, want %v", tc.input, ip.String(), tc.wantIP)
		}
		// Decimal IPs must also be blocked if they represent blocked addresses
		if tc.wantIP == "127.0.0.1" {
			if got := ssrf.Classify(ip); got != ssrf.Blocked {
				t.Errorf("Classify(ParseDecimalIP(%q)) = %v, want BLOCKED", tc.input, got)
			}
		}
	}
}

func TestClassifyAddr(t *testing.T) {
	cases := []struct {
		addr string
		want ssrf.Class
	}{
		{"127.0.0.1:80", ssrf.Blocked},
		{"8.8.8.8:443", ssrf.Allowed},
		{"[::1]:80", ssrf.Blocked},
		{"not-an-addr", ssrf.Blocked},
	}
	for _, tc := range cases {
		got := ssrf.ClassifyAddr("tcp", tc.addr)
		if got != tc.want {
			t.Errorf("ClassifyAddr(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}
