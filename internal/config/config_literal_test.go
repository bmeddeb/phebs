package config

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

func TestParseLiteralRejectsDecodedAmbientValues(t *testing.T) {
	t.Setenv("PHEBS_LITERAL_TEST", "ambient-value")
	prefix := "server: {data_dir: /tmp/literal-config}\n"
	for _, value := range []string{
		`'${PHEBS_LITERAL_TEST}'`,
		`"\u0024{PHEBS_LITERAL_TEST}"`,
		`"\x24{PHEBS_LITERAL_TEST}"`,
		`"\U00000024{PHEBS_LITERAL_TEST}"`,
		"!!binary " + base64.StdEncoding.EncodeToString([]byte("${PHEBS_LITERAL_TEST}")),
	} {
		t.Run(value, func(t *testing.T) {
			raw := []byte(prefix + "auth: {api_key: " + value + "}\n")
			ordinary, err := Parse(raw)
			if err != nil || ordinary.Auth.APIKey != "ambient-value" {
				t.Fatal("fixture did not exercise decoded ordinary expansion", err)
			}
			if cfg, err := ParseLiteral(raw); cfg != nil || err == nil || !strings.Contains(err.Error(), "dollar sign") {
				t.Fatal("decoded environment reference admitted", err)
			}
		})
	}
	for _, body := range []string{
		"auth: {api_key: &secret \"\\u0024{PHEBS_LITERAL_TEST}\"}\nwebhook: {secret: *secret}\n",
		"auth:\n  api_key: |\n    ${PHEBS_LITERAL_TEST}\n",
	} {
		if cfg, err := ParseLiteral([]byte(prefix + body)); cfg != nil || err == nil || !strings.Contains(err.Error(), "dollar sign") {
			t.Fatal("aliased or block scalar environment reference admitted", err)
		}
	}
}

func TestParseLiteralRequiresExplicitDataDirectory(t *testing.T) {
	for _, raw := range []string{
		"{}", "server: {}", "server: {data_dir: null}", "server: {data_dir: ''}",
		"server: {data_dir: .}", "server: {data_dir: relative/data}",
		"server: {data_dir: '~'}", "server: {data_dir: '~/data'}",
		"server: {data_dir: /tmp//data}", "server: {data_dir: /tmp/../data}",
	} {
		t.Run(raw, func(t *testing.T) {
			if cfg, err := ParseLiteral([]byte(raw)); cfg != nil || err == nil || !strings.Contains(err.Error(), "explicit clean absolute") {
				t.Fatal("ambient/default/normalized data directory admitted", err)
			}
			if _, err := Parse([]byte(raw)); err != nil {
				t.Fatal("ordinary config lost its existing path resolution", err)
			}
		})
	}
}

func TestParseLiteralReusesStrictValidationAndDefaults(t *testing.T) {
	raw := []byte("# $ in a comment is not a decoded value\nserver: {data_dir: /tmp/literal-config}\nauth: {api_key: literal}\n")
	ordinary, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	literal, err := ParseLiteral(raw)
	if err != nil || !reflect.DeepEqual(literal, ordinary) {
		t.Fatal("literal config changed deterministic validation/defaults", err)
	}
	for _, raw := range []string{
		"server: {data_dir: /tmp/literal-config, unknown: true}",
		"server: {data_dir: [/tmp/literal-config]}",
		"server: {data_dir: /tmp/literal-config}\nsync: {poll_interval: invalid}",
	} {
		if cfg, err := ParseLiteral([]byte(raw)); cfg != nil || err == nil {
			t.Fatal("literal parse bypassed normal strict validation", err)
		}
	}
}
