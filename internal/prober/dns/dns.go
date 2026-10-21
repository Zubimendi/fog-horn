// Package dnsprober implements DNS resolve + assertion checks.
package dnsprober

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/zubimendi/fog-horn/internal/model"
)

// RecordType enumerates the supported DNS query types.
type RecordType string

const (
	TypeA    RecordType = "A"
	TypeAAAA RecordType = "AAAA"
	TypeCNAME RecordType = "CNAME"
	TypeMX   RecordType = "MX"
	TypeTXT  RecordType = "TXT"
)

// Config holds the DNS check configuration.
type Config struct {
	Hostname   string
	RecordType RecordType
	// AssertValue, if set, checks that at least one record matches.
	AssertValue string
	Timeout     time.Duration
}

// Result is the output of a single DNS check.
type Result struct {
	Class  model.CheckClass
	Reason string
	DNSMS  float64
}

// Check resolves the hostname and asserts the expected record type/value.
func Check(ctx context.Context, cfg Config) Result {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resolver := net.DefaultResolver
	start := time.Now()
	var err error
	var found bool

	switch cfg.RecordType {
	case TypeA, TypeAAAA:
		var addrs []string
		addrs, err = resolver.LookupHost(ctx, cfg.Hostname)
		dnsMS := float64(time.Since(start)) / float64(time.Millisecond)
		if err != nil {
			return Result{Class: model.ClassTargetFailure, Reason: "DNS: " + err.Error(), DNSMS: dnsMS}
		}
		if cfg.AssertValue != "" {
			for _, a := range addrs {
				if a == cfg.AssertValue {
					found = true
					break
				}
			}
			if !found {
				return Result{Class: model.ClassTargetFailure,
					Reason: fmt.Sprintf("DNS: no record matching %q", cfg.AssertValue), DNSMS: dnsMS}
			}
		}
		return Result{Class: model.ClassOK, DNSMS: dnsMS}

	case TypeCNAME:
		var cname string
		cname, err = resolver.LookupCNAME(ctx, cfg.Hostname)
		dnsMS := float64(time.Since(start)) / float64(time.Millisecond)
		if err != nil {
			return Result{Class: model.ClassTargetFailure, Reason: "DNS: " + err.Error(), DNSMS: dnsMS}
		}
		if cfg.AssertValue != "" && cname != cfg.AssertValue {
			return Result{Class: model.ClassTargetFailure,
				Reason: fmt.Sprintf("DNS: CNAME %q != %q", cname, cfg.AssertValue), DNSMS: dnsMS}
		}
		return Result{Class: model.ClassOK, DNSMS: dnsMS}

	case TypeTXT:
		var records []string
		records, err = resolver.LookupTXT(ctx, cfg.Hostname)
		dnsMS := float64(time.Since(start)) / float64(time.Millisecond)
		if err != nil {
			return Result{Class: model.ClassTargetFailure, Reason: "DNS: " + err.Error(), DNSMS: dnsMS}
		}
		if cfg.AssertValue != "" {
			for _, r := range records {
				if r == cfg.AssertValue {
					found = true
					break
				}
			}
			if !found {
				return Result{Class: model.ClassTargetFailure,
					Reason: fmt.Sprintf("DNS: no TXT record matching %q", cfg.AssertValue), DNSMS: dnsMS}
			}
		}
		return Result{Class: model.ClassOK, DNSMS: dnsMS}

	default:
		return Result{Class: model.ClassConfigError, Reason: "unsupported record type: " + string(cfg.RecordType)}
	}
}
