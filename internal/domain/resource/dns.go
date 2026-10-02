// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// DNS resource kinds.
const (
	KindDNSZone   = "dns_zone"
	KindDNSRecord = "dns_record"
)

// dnsRecordTypes is the set of supported record types.
var dnsRecordTypes = map[string]bool{
	"A": true, "AAAA": true, "CNAME": true, "TXT": true,
	"MX": true, "NS": true, "SRV": true, "PTR": true, "CAA": true,
}

// DNSZoneSpec is the desired state of a DNS zone. A private zone is resolvable
// only from the VPCs it is attached to.
type DNSZoneSpec struct {
	Name   string `json:"name,omitempty"`
	Domain string `json:"domain"`
	// Visibility is "public" or "private" (default "private").
	Visibility string   `json:"visibility,omitempty"`
	VPCIDs     []string `json:"vpcIds,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s DNSZoneSpec) Validate() error {
	if s.Domain == "" {
		return fmt.Errorf("dns_zone: domain is required")
	}
	switch s.Visibility {
	case "", "public", "private":
	default:
		return fmt.Errorf("dns_zone: visibility must be public or private")
	}
	return nil
}

// DNSZoneStatus is the observed state of a DNS zone.
type DNSZoneStatus struct {
	StatusBase
}

// DNSZone is a DNS zone resource.
type DNSZone = Resource[DNSZoneSpec, DNSZoneStatus]

// DNSRecordSpec is the desired state of a DNS record within a zone.
type DNSRecordSpec struct {
	ZoneID  string   `json:"zoneId"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	TTL     int      `json:"ttl,omitempty"`
	Records []string `json:"records"`
}

// Validate reports whether the spec is well-formed.
func (s DNSRecordSpec) Validate() error {
	if s.ZoneID == "" {
		return fmt.Errorf("dns_record: zoneId is required")
	}
	if !dnsRecordTypes[s.Type] {
		return fmt.Errorf("dns_record: unsupported type %q", s.Type)
	}
	if len(s.Records) == 0 {
		return fmt.Errorf("dns_record: at least one record value is required")
	}
	if s.TTL < 0 {
		return fmt.Errorf("dns_record: ttl must not be negative")
	}
	return nil
}

// DNSRecordStatus is the observed state of a DNS record.
type DNSRecordStatus struct {
	StatusBase
}

// DNSRecord is a DNS record resource.
type DNSRecord = Resource[DNSRecordSpec, DNSRecordStatus]
