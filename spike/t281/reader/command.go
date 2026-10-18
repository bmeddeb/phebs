package main

import (
	"encoding/json"
	"go/ast"
	"strconv"
	"strings"
)

type commandSpec struct {
	Container string `json:"container"`
	KeySpecs  []struct {
		Flags []string `json:"flags"`
		Begin struct {
			Index *struct {
				Pos int `json:"pos"`
			} `json:"index"`
			Keyword *struct {
				Keyword string `json:"keyword"`
				Start   int    `json:"startfrom"`
			} `json:"keyword"`
		} `json:"begin_search"`
		Find struct {
			Range *struct {
				Last  int `json:"lastkey"`
				Step  int `json:"step"`
				Limit int `json:"limit"`
			} `json:"range"`
			Keynum *struct {
				Index int `json:"keynumidx"`
				First int `json:"firstkey"`
				Step  int `json:"step"`
			} `json:"keynum"`
		} `json:"find_keys"`
	} `json:"key_specs"`
}

var methodCommands = map[string]string{
	"SAdd": "SADD", "SMembers": "SMEMBERS", "SIsMember": "SISMEMBER", "SRem": "SREM", "ZAdd": "ZADD", "ZAddXX": "ZADD", "ZScore": "ZSCORE", "ZRem": "ZREM", "ZRevRangeWithScores": "ZREVRANGE", "Del": "DEL", "HSet": "HSET", "HVals": "HVALS", "Get": "GET", "Set": "SET", "SetNX": "SETNX", "LRange": "LRANGE", "Info": "INFO", "Ping": "PING", "ClusterInfo": "CLUSTER INFO", "ClusterKeySlot": "CLUSTER KEYSLOT", "ClusterSlots": "CLUSTER SLOTS", "Publish": "PUBLISH", "Subscribe": "SUBSCRIBE", "Eval": "EVAL", "EvalSha": "EVALSHA", "FCall": "FCALL",
}
var expectedKinds = map[string]string{"GET": "string", "SET": "string", "SETNX": "string", "HSET": "hash", "HGET": "hash", "HVALS": "hash", "LPUSH": "list", "LRANGE": "list", "SADD": "set", "SMEMBERS": "set", "SISMEMBER": "set", "SREM": "set", "ZADD": "sorted-set", "ZSCORE": "sorted-set", "ZREM": "sorted-set", "ZREVRANGE": "sorted-set", "XREAD": "stream", "XADD": "stream", "XGROUP CREATE": "stream"}

func (e *engine) addCommand(raw []byte) error {
	var parsed map[string]commandSpec
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return err
	}
	for name, spec := range parsed {
		key := name
		if spec.Container != "" {
			key = spec.Container + " " + name
		}
		if _, ok := e.commands[key]; ok {
			return &syntaxError{}
		}
		e.commands[key] = spec
	}
	return nil
}

type syntaxError struct{}

func (*syntaxError) Error() string { return "unsupported metadata shape" }
func word(text string) value       { return value{Bytes: []byte(text), State: "literal"} }
func (g *goFile) typedVector(method string, call *ast.CallExpr, steps *int) []value {
	command := methodCommands[method]
	args := []value{}
	for _, token := range strings.Split(command, " ") {
		args = append(args, word(token))
	}
	if len(call.Args) < 1 {
		return nil
	}
	if method == "SetNX" {
		if len(call.Args) != 4 {
			return nil
		}
		expiry := g.literal(call.Args[3], steps)
		if expiry.State != "literal" || !expiry.Integer || string(expiry.Bytes) != "0" {
			return nil
		}
	}
	if method == "Eval" || method == "EvalSha" || method == "FCall" {
		if len(call.Args) < 3 {
			return nil
		}
		args = append(args, g.literal(call.Args[1], steps))
		list, ok := g.array(call.Args[2])
		if !ok {
			return append(args, value{State: "unresolved", Reason: "opaque-helper", Span: g.at(call.Args[2])})
		}
		args = append(args, word(strconv.Itoa(len(list))))
		for _, x := range list {
			args = append(args, g.literal(x, steps))
		}
		return args
	}
	end := len(call.Args)
	if method == "Set" || method == "SetNX" {
		end = min(end, 3)
	}
	for i, x := range call.Args[1:end] {
		if method == "Del" && call.Ellipsis.IsValid() && i == end-2 {
			if list, ok := g.array(x); ok {
				for _, item := range list {
					args = append(args, g.literal(item, steps))
				}
				continue
			}
		}
		args = append(args, g.literal(x, steps))
	}
	return args
}
func contains(words []string, want string) bool {
	for _, w := range words {
		if w == want {
			return true
		}
	}
	return false
}
func decimal(v value, maxValue int) (int, bool) {
	if v.State != "literal" || len(v.Bytes) == 0 || len(v.Bytes) > 10 {
		return 0, false
	}
	if len(v.Bytes) > 1 && v.Bytes[0] == '0' {
		return 0, false
	}
	n := 0
	for _, b := range v.Bytes {
		if b < '0' || b > '9' || n > (maxValue-int(b-'0'))/10 {
			return 0, false
		}
		n = n*10 + int(b-'0')
	}
	return n, n <= maxValue
}
func canonicalDecimal(v value) bool {
	if v.State != "literal" || len(v.Bytes) == 0 || (len(v.Bytes) > 1 && v.Bytes[0] == '0') {
		return false
	}
	for _, b := range v.Bytes {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}
func upper(raw []byte) string {
	out := append([]byte{}, raw...)
	for i, b := range out {
		if b >= 'a' && b <= 'z' {
			out[i] = b - 'a' + 'A'
		}
	}
	return string(out)
}
func (e *engine) commandRow(row *site, args []value, method string, b *blobResult) {
	if len(args) > 256 {
		b.Boundary = "resource-limit"
		return
	}
	if args[0].State != "literal" {
		row.Gaps = []string{args[0].Reason}
		return
	}
	name := upper(args[0].Bytes)
	if len(args) > 1 && args[1].State == "literal" {
		if _, ok := e.commands[name+" "+upper(args[1].Bytes)]; ok {
			name += " " + upper(args[1].Bytes)
		}
	}
	row.Command = name
	if name == "FT.CREATE" || name == "TS.CREATE" || name == "TS.CREATERULE" {
		row.Recognized = true
		n, reason := nativeVector(name, args, row.Span)
		if reason == "resource-limit" {
			b.Boundary = reason
		} else if reason != "" {
			row.Gaps = gaps(reason)
		} else {
			b.Native = append(b.Native, n)
		}
		return
	}
	spec, ok := e.commands[name]
	if !ok {
		reason := "unsupported-core-command-after-pin"
		if strings.Contains(name, ".") {
			reason = "unsupported-module-command"
		}
		row.Gaps = gaps(reason)
		return
	}
	row.Recognized = true
	script := name == "EVAL" || name == "EVALSHA" || name == "FCALL"
	if script && len(args) > 2 && args[2].State == "unresolved" && args[2].Reason != "" {
		row.Gaps = gaps(args[2].Reason)
		row.KeyCardinality = "unmeasured"
		return
	}
	for _, keyspec := range spec.KeySpecs {
		if contains(keyspec.Flags, "NOT_KEY") {
			continue
		}
		begin, valid := 0, false
		if keyspec.Begin.Index != nil {
			begin = keyspec.Begin.Index.Pos
			if begin < 0 {
				begin += len(args)
			}
			valid = begin >= 0 && begin < len(args)
		}
		if kw := keyspec.Begin.Keyword; kw != nil {
			start := kw.Start
			if start < 0 {
				start += len(args)
			}
			step := 1
			if kw.Start < 0 {
				step = -1
			}
			for i := start; i >= 0 && i < len(args); i += step {
				if args[i].State != "literal" {
					break
				}
				if upper(args[i].Bytes) == kw.Keyword {
					begin = i + 1
					valid = begin < len(args)
					break
				}
			}
		}
		if !valid {
			row.Gaps = gaps(append(row.Gaps, "incomplete-key-specification")...)
			continue
		}
		positions := []int{}
		if r := keyspec.Find.Range; r != nil && r.Step > 0 {
			last := begin + r.Last
			if r.Last < 0 {
				last = len(args) + r.Last
			}
			if r.Limit > 0 {
				n := len(args) - begin
				if n%r.Limit != 0 {
					valid = false
				} else {
					last = begin + n/r.Limit - 1
				}
			}
			if last >= len(args) || last < begin {
				valid = false
			} else {
				for p := begin; p <= last; p += r.Step {
					positions = append(positions, p)
				}
			}
		} else if k := keyspec.Find.Keynum; k != nil && k.Step > 0 {
			index := begin + k.Index
			if index < 0 || index >= len(args) {
				valid = false
			} else {
				count, ok := decimal(args[index], 64)
				if !ok && canonicalDecimal(args[index]) {
					b.Boundary = "resource-limit"
					return
				}
				valid = ok
				if !ok && args[index].Reason != "" {
					row.Gaps = gaps(append(row.Gaps, args[index].Reason)...)
					row.KeyCardinality = "unmeasured"
				}
				if valid {
					for i := 0; i < count; i++ {
						p := begin + k.First + i*k.Step
						if p >= len(args) {
							valid = false
							break
						}
						positions = append(positions, p)
					}
				}
			}
		} else {
			valid = false
		}
		if len(positions) > 64 {
			b.Boundary = "resource-limit"
			return
		}
		if !valid {
			row.Gaps = gaps(append(row.Gaps, "incomplete-key-specification")...)
			continue
		}
		flags := []string{}
		for _, flag := range keyspec.Flags {
			if flag != "VARIABLE_FLAGS" && flag != "INCOMPLETE" {
				flags = append(flags, flag)
			}
		}
		if contains(keyspec.Flags, "INCOMPLETE") {
			row.Gaps = gaps(append(row.Gaps, "incomplete-key-specification")...)
		}
		if contains(keyspec.Flags, "VARIABLE_FLAGS") {
			proved := name == "SET"
			get := false
			if method != "Set" {
				for i := 3; i < len(args); i++ {
					if args[i].State != "literal" {
						proved = false
						break
					}
					option := upper(args[i].Bytes)
					switch option {
					case "GET":
						get = true
					case "NX", "XX", "KEEPTTL":
					case "EX", "PX", "EXAT", "PXAT":
						i++
						if i >= len(args) {
							proved = false
						}
					default:
						proved = false
					}
				}
			}
			if !proved {
				flags = nil
				row.Gaps = gaps(append(row.Gaps, "variable-access-mode")...)
			} else if get {
				flags = []string{"RW", "ACCESS", "UPDATE"}
			} else {
				flags = []string{"OW", "UPDATE"}
			}
		}
		for _, p := range positions {
			v := args[p]
			if v.TooLarge {
				b.Boundary = "resource-limit"
				return
			}
			if v.Integer {
				v.State = "unresolved"
				v.Reason = "opaque-helper"
				v.Bytes = nil
			}
			if script && v.State != "literal" && v.State != "structure" {
				row.Gaps = gaps(append(row.Gaps, v.Reason)...)
				continue
			}
			k := keyFact{Predicate: "OBSERVES_REDIS_KEY_ARGUMENT", Position: p, Value: v, Access: flags, ExpectedKind: expectedKinds[name], ScriptPass: script}
			if script {
				k.Predicate = "PASSES_REDIS_KEY_TO_SCRIPT"
				k.Access = nil
				k.ExpectedKind = ""
			} else if k.ExpectedKind == "" {
				k.KindGap = "unknown-expected-value-kind"
				row.Gaps = gaps(append(row.Gaps, k.KindGap)...)
			}
			if v.State == "literal" {
				k.HashTag = hashTag(v.Bytes)
				k.HashTagState = "literal-byte-selection"
			} else if v.State == "structure" {
				k.HashTagState = "argument-dependent; parameter may contain closing brace"
			} else {
				row.Gaps = gaps(append(row.Gaps, v.Reason)...)
			}
			row.Keys = append(row.Keys, k)
			if len(row.Keys) > 64 {
				b.Boundary = "resource-limit"
				return
			}
		}
	}
	if name == "XGROUP CREATE" {
		if n, reason := streamGroup(args, row.Span); reason == "" {
			b.Native = append(b.Native, n)
		} else if reason == "resource-limit" {
			b.Boundary = reason
		} else {
			row.Gaps = gaps(append(row.Gaps, reason)...)
		}
	}
}
func hashTag(key []byte) []byte {
	left := strings.IndexByte(string(key), '{')
	if left >= 0 {
		if right := strings.IndexByte(string(key[left+1:]), '}'); right > 0 {
			return append([]byte{}, key[left+1:left+1+right]...)
		}
	}
	return append([]byte{}, key...)
}
