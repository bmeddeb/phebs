package main

import (
	"encoding/base64"
	"strings"
)

func nameValue(v value) bool {
	return v.State == "literal" && !v.Integer && len(v.Bytes) > 0 && len(v.Bytes) <= 4096
}
func nativeVector(command string, a []value, source span) (nativeFact, string) {
	n := nativeFact{Source: source, Options: map[string]string{}}
	for _, v := range a {
		if v.TooLarge {
			return nativeFact{}, "resource-limit"
		}
		if v.State != "literal" {
			if v.Reason != "" {
				return nativeFact{}, v.Reason
			}
			return nativeFact{}, "opaque-helper"
		}
	}
	bad := func() (nativeFact, string) { return nativeFact{}, "unsupported-module-command" }
	if len(a) < 2 || !nameValue(a[1]) {
		return bad()
	}
	n.Name = append([]byte{}, a[1].Bytes...)
	switch command {
	case "FT.CREATE":
		if len(a) < 9 || upper(a[2].Bytes) != "ON" || (upper(a[3].Bytes) != "HASH" && upper(a[3].Bytes) != "JSON") || upper(a[4].Bytes) != "PREFIX" {
			return bad()
		}
		count, ok := decimal(a[5], 8)
		if !ok || count < 1 || 6+count >= len(a) || upper(a[6+count].Bytes) != "SCHEMA" {
			return bad()
		}
		n.Kind = "index"
		n.Options["on"] = upper(a[3].Bytes)
		prefixes := map[string]bool{}
		for _, v := range a[6 : 6+count] {
			if !nameValue(v) || prefixes[string(v.Bytes)] {
				return bad()
			}
			prefixes[string(v.Bytes)] = true
			n.Prefixes = append(n.Prefixes, append([]byte{}, v.Bytes...))
		}
		seen := map[string]bool{}
		p := 7 + count
		for p < len(a) {
			if len(n.Fields) >= 32 || p+1 >= len(a) || !nameValue(a[p]) || seen[string(a[p].Bytes)] {
				return bad()
			}
			seen[string(a[p].Bytes)] = true
			field := nativeField{Name: append([]byte{}, a[p].Bytes...), Span: a[p].Span, Kind: upper(a[p+1].Bytes)}
			p += 2
			switch field.Kind {
			case "TEXT", "TAG", "NUMERIC":
			case "VECTOR":
				if p+8 > len(a) || upper(a[p].Bytes) != "FLAT" {
					return bad()
				}
				attributes, ok := decimal(a[p+1], 6)
				if !ok || attributes != 6 {
					return bad()
				}
				p += 2
				opts := map[string]string{}
				for end := p + 6; p < end; p += 2 {
					key := upper(a[p].Bytes)
					if _, duplicate := opts[key]; duplicate {
						return bad()
					}
					switch key {
					case "TYPE":
						if upper(a[p+1].Bytes) != "FLOAT32" {
							return bad()
						}
						opts[key] = "FLOAT32"
					case "DIM":
						d, ok := decimal(a[p+1], 4096)
						if !ok || d < 1 {
							return bad()
						}
						opts[key] = string(a[p+1].Bytes)
					case "DISTANCE_METRIC":
						metric := upper(a[p+1].Bytes)
						if metric != "L2" && metric != "IP" && metric != "COSINE" {
							return bad()
						}
						opts[key] = metric
					default:
						return bad()
					}
				}
				if len(opts) != 3 {
					return bad()
				}
				field.Options = opts
			default:
				return bad()
			}
			n.Fields = append(n.Fields, field)
		}
		if len(n.Fields) == 0 {
			return bad()
		}
	case "TS.CREATE":
		n.Kind = "series"
		p := 2
		if p < len(a) && upper(a[p].Bytes) == "RETENTION" {
			if p+1 >= len(a) {
				return bad()
			}
			if _, ok := decimal(a[p+1], 2147483647); !ok {
				return bad()
			}
			n.Options["retention_milliseconds"] = string(a[p+1].Bytes)
			p += 2
		}
		if p < len(a) {
			if upper(a[p].Bytes) != "LABELS" {
				return bad()
			}
			p++
			if (len(a)-p)%2 != 0 || len(a)-p < 2 || len(a)-p > 16 {
				return bad()
			}
			names := map[string]bool{}
			for ; p < len(a); p += 2 {
				if !nameValue(a[p]) || len(a[p+1].Bytes) > 4096 || names[string(a[p].Bytes)] {
					return bad()
				}
				names[string(a[p].Bytes)] = true
				n.Options["label_base64:"+base64.StdEncoding.EncodeToString(a[p].Bytes)] = base64.StdEncoding.EncodeToString(a[p+1].Bytes)
			}
		}
	case "TS.CREATERULE":
		if len(a) != 6 || !nameValue(a[2]) || string(a[1].Bytes) == string(a[2].Bytes) || upper(a[3].Bytes) != "AGGREGATION" {
			return bad()
		}
		aggregate := upper(a[4].Bytes)
		if aggregate != "AVG" && aggregate != "SUM" {
			return bad()
		}
		bucket, ok := decimal(a[5], 2147483647)
		if !ok || bucket < 1 {
			return bad()
		}
		n.Kind = "series-rule"
		n.References = [][]byte{append([]byte{}, a[1].Bytes...), append([]byte{}, a[2].Bytes...)}
		n.Options["aggregation"] = strings.ToLower(aggregate)
		n.Options["bucket_milliseconds"] = string(a[5].Bytes)
	default:
		return bad()
	}
	return n, ""
}
func streamGroup(a []value, source span) (nativeFact, string) {
	if len(a) != 5 && len(a) != 6 {
		return nativeFact{}, "incomplete-key-specification"
	}
	for _, v := range a {
		if v.TooLarge {
			return nativeFact{}, "resource-limit"
		}
		if v.State != "literal" {
			return nativeFact{}, v.Reason
		}
	}
	if !nameValue(a[2]) || !nameValue(a[3]) || len(a[4].Bytes) == 0 || (len(a) == 6 && upper(a[5].Bytes) != "MKSTREAM") {
		return nativeFact{}, "incomplete-key-specification"
	}
	return nativeFact{Kind: "stream-group", Name: append([]byte{}, a[3].Bytes...), Source: source, References: [][]byte{append([]byte{}, a[2].Bytes...)}, Options: map[string]string{"start_id": string(a[4].Bytes)}}, ""
}
