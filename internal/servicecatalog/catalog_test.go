package servicecatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestCanonicalCatalogDeterministic(t *testing.T) {
	t.Parallel()

	first := neutralCatalog()
	firstCanonical, err := Canonical(first)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(firstCanonical) {
		t.Fatalf("canonical catalog is not JSON: %s", firstCanonical)
	}
	firstDigest, err := Digest(first)
	if err != nil {
		t.Fatal(err)
	}

	second := neutralCatalog()
	slices.Reverse(second.Services)
	slices.Reverse(second.Memberships)
	slices.Reverse(second.Unowned)
	slices.Reverse(second.Services[1].Successors)
	secondCanonical, err := Canonical(second)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := Digest(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstCanonical, secondCanonical) {
		t.Fatalf("canonical catalogs differ:\n%s\n%s", firstCanonical, secondCanonical)
	}
	if firstDigest != secondDigest {
		t.Fatalf("digest %q != %q", firstDigest, secondDigest)
	}
	const wantDigest = "sha256:e3b6610bbf0fd6b364d6a8a3704f36fabc291734942bfd08b10dab9733575079"
	if firstDigest != wantDigest {
		t.Fatalf("digest = %q, want %q", firstDigest, wantDigest)
	}

	if first.Services[1].Successors[0] != "orders" || first.Services[1].Successors[1] != "payments" {
		t.Fatal("Canonical mutated its input successor order")
	}
	decoded, err := Decode(firstCanonical)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := Canonical(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstCanonical, reencoded) {
		t.Fatal("canonical decode did not round trip")
	}
}

func TestCanonicalCatalogUsesJSONEscapesForNonPrintableAstralRunes(t *testing.T) {
	t.Parallel()

	catalog := neutralCatalog()
	catalog.Services[0].DisplayName = "a\U000e0001b"
	catalog.Services[2].Reason = "observed \U000e0001 marker"
	canonical, err := Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(canonical) {
		t.Fatalf("canonical catalog is not JSON: %q", canonical)
	}
	if bytes.Contains(canonical, []byte(`\U000e0001`)) {
		t.Fatalf("canonical catalog contains a Go-only escape: %q", canonical)
	}
	decoded, err := Decode(canonical)
	if err != nil {
		t.Fatal(err)
	}
	services := make(map[string]Service, len(decoded.Services))
	for _, service := range decoded.Services {
		services[service.Key] = service
	}
	if services["orders"].DisplayName != catalog.Services[0].DisplayName {
		t.Fatalf("display name = %q, want %q", services["orders"].DisplayName, catalog.Services[0].DisplayName)
	}
	if services["suggested"].Reason != catalog.Services[2].Reason {
		t.Fatalf("reason = %q, want %q", services["suggested"].Reason, catalog.Services[2].Reason)
	}
}

func TestDecodeStrictClosedWire(t *testing.T) {
	t.Parallel()

	canonical, err := Canonical(neutralCatalog())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		content []byte
	}{
		{name: "top-level unknown", content: replaceOnce(t, canonical, `"schema":`, `"extra":true,"schema":`)},
		{name: "nested unknown", content: replaceOnce(t, canonical, `"kind":"committed"`, `"extra":true,"kind":"committed"`)},
		{name: "top-level duplicate", content: replaceOnce(t, canonical, `"schema":`, `"schema":"duplicate","schema":`)},
		{name: "nested duplicate", content: replaceOnce(t, canonical, `"kind":"committed"`, `"kind":"operator","kind":"committed"`)},
		{name: "trailing value", content: append(slices.Clone(canonical), []byte(` {}`)...)},
		{name: "null catalog", content: []byte(`null`)},
		{name: "null collection", content: replaceOnce(t, canonical, `"services":[`, `"services":null,"ignored":[`)},
		{name: "null optional string", content: replaceOnce(t, canonical, `"reason":"candidate only"`, `"reason":null`)},
		{name: "missing required", content: withoutUnowned(t, canonical)},
		{name: "invalid utf8", content: append([]byte(`{"schema":"`), 0xff)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Decode(test.content); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Decode() error = %v, want ErrInvalid", err)
			}
		})
	}

	oversized := make([]byte, MaxEncodedBytes+1)
	if _, err := Decode(oversized); !errors.Is(err, ErrEncodedLimit) || errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized Decode() error = %v, want only ErrEncodedLimit", err)
	}
}

func TestDecodeStopsBeforeCollectionGrowthPastLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field string
		item  string
		count int
		want  error
	}{
		{
			name: "services", field: "services",
			item:  `{"key":"s","display_name":"S","disposition":"proposal","origin":"base","reason":"r"}`,
			count: MaxServices + 1, want: ErrServiceLimit,
		},
		{
			name: "memberships", field: "memberships",
			item:  `{"service_key":"s","path":"p","role":"primary","origin":"base"}`,
			count: MaxMemberships + 1, want: ErrMembershipLimit,
		},
		{
			name: "unowned", field: "unowned",
			item:  `{"path":"p","origin":"base"}`,
			count: MaxDistinctPaths + 1, want: ErrDistinctPathLimit,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			content := collectionInput(test.field, test.item, test.count)
			if len(content) > MaxEncodedBytes {
				t.Fatalf("test input has %d bytes", len(content))
			}
			_, err := Decode(content)
			if !errors.Is(err, test.want) {
				t.Fatalf("Decode() error = %v, want %v", err, test.want)
			}
		})
	}

	var builder strings.Builder
	builder.WriteString(`{"schema":"phebs-service-catalog-v2","authority":{"kind":"operator","id":"owner","version":"v1"},"services":[`)
	for service := range MaxServices {
		if service != 0 {
			builder.WriteByte(',')
		}
		fmt.Fprintf(&builder, `{"key":"s%d","display_name":"S","disposition":"rejected","origin":"base","reason":"r","successors":[`, service)
		if service == 0 {
			builder.WriteString(`"target","second"`)
		} else {
			builder.WriteString(`"target"`)
		}
		builder.WriteString(`]}`)
	}
	builder.WriteString(`],"memberships":[],"unowned":[]}`)
	if builder.Len() > MaxEncodedBytes {
		t.Fatalf("successor-limit input has %d bytes", builder.Len())
	}
	if _, err := Decode([]byte(builder.String())); !errors.Is(err, ErrSuccessorLimit) {
		t.Fatalf("successor Decode() error = %v, want ErrSuccessorLimit", err)
	}
}

func TestCatalogSemanticRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Catalog)
		want   error
	}{
		{name: "wrong schema", mutate: func(c *Catalog) { c.Schema = "future" }, want: ErrInvalid},
		{name: "unknown authority", mutate: func(c *Catalog) { c.Authority.Kind = "detected" }, want: ErrInvalid},
		{name: "bad committed version", mutate: func(c *Catalog) { c.Authority.Version = "abc" }, want: ErrInvalid},
		{name: "invalid authority id", mutate: func(c *Catalog) { c.Authority.ID = "bad id" }, want: ErrInvalid},
		{name: "override origin without authority", mutate: func(c *Catalog) { c.Override = nil }, want: ErrInvalid},
		{name: "bad service key", mutate: func(c *Catalog) { c.Services[0].Key = "bad/key" }, want: ErrInvalid},
		{name: "duplicate service", mutate: func(c *Catalog) { c.Services = append(c.Services, c.Services[0]) }, want: ErrInvalid},
		{name: "accepted reason", mutate: func(c *Catalog) { c.Services[0].Reason = "not allowed" }, want: ErrInvalid},
		{name: "proposal without reason", mutate: func(c *Catalog) { c.Services[2].Reason = "" }, want: ErrInvalid},
		{name: "unknown membership service", mutate: func(c *Catalog) { c.Memberships[0].ServiceKey = "absent" }, want: ErrInvalid},
		{name: "unsafe path", mutate: func(c *Catalog) { c.Memberships[0].Path = "../escape" }, want: ErrInvalid},
		{name: "unknown role", mutate: func(c *Catalog) { c.Memberships[0].Role = "owner" }, want: ErrInvalid},
		{name: "duplicate triple", mutate: func(c *Catalog) { c.Memberships = append(c.Memberships, c.Memberships[0]) }, want: ErrInvalid},
		{name: "accepted without primary", mutate: func(c *Catalog) { c.Memberships[0].Role = RoleShared }, want: ErrInvalid},
		{name: "typed without supporting", mutate: func(c *Catalog) { c.Memberships[1].Role = RoleGenerated }, want: ErrInvalid},
		{name: "same service prefix overlap", mutate: func(c *Catalog) {
			c.Memberships = append(c.Memberships, Membership{ServiceKey: "orders", Path: "services/orders", Role: RoleShared, Origin: OriginBase})
		}, want: ErrInvalid},
		{name: "duplicate unowned", mutate: func(c *Catalog) { c.Unowned = append(c.Unowned, c.Unowned[0]) }, want: ErrInvalid},
		{name: "unowned accepted overlap", mutate: func(c *Catalog) { c.Unowned[0].Path = "services/orders" }, want: ErrInvalid},
		{name: "self successor", mutate: func(c *Catalog) { c.Services[1].Successors = []string{"legacy"} }, want: ErrInvalid},
		{name: "unknown successor", mutate: func(c *Catalog) { c.Services[1].Successors = []string{"absent"} }, want: ErrInvalid},
		{name: "successor proposal", mutate: func(c *Catalog) { c.Services[1].Successors = []string{"suggested"} }, want: ErrInvalid},
		{name: "successor cycle", mutate: func(c *Catalog) {
			c.Services[1].Successors = []string{"older"}
			c.Services = append(c.Services, Service{Key: "older", DisplayName: "Older", Disposition: DispositionRejected, Origin: OriginBase, Reason: "old", Successors: []string{"legacy"}})
		}, want: ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			catalog := neutralCatalog()
			test.mutate(&catalog)
			if err := Validate(catalog); !errors.Is(err, test.want) {
				t.Fatalf("Validate() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCatalogAllowsExactManyToManyAndCrossServicePrefixes(t *testing.T) {
	t.Parallel()

	catalog := neutralCatalog()
	catalog.Memberships = append(catalog.Memberships,
		Membership{ServiceKey: "payments", Path: "services/orders", Role: RoleShared, Origin: OriginBase},
	)
	if err := Validate(catalog); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogIndependentLimits(t *testing.T) {
	t.Parallel()

	t.Run("fanout", func(t *testing.T) {
		catalog := fanoutCatalog(MaxAcceptedPathFanout + 1)
		if err := Validate(catalog); !errors.Is(err, ErrFanoutLimit) {
			t.Fatalf("Validate() error = %v, want ErrFanoutLimit", err)
		}
	})
	t.Run("distinct paths", func(t *testing.T) {
		catalog := Catalog{
			Schema:    Schema,
			Authority: Authority{Kind: AuthorityOperator, ID: "owner", Version: "v1"},
			Services:  []Service{}, Memberships: []Membership{},
			Unowned: make([]UnownedPlacement, MaxDistinctPaths+1),
		}
		if err := Validate(catalog); !errors.Is(err, ErrDistinctPathLimit) {
			t.Fatalf("Validate() error = %v, want ErrDistinctPathLimit", err)
		}
	})
	t.Run("per-service path count", func(t *testing.T) {
		catalog := oneServiceCatalog()
		for index := 1; index <= MaxServicePaths; index++ {
			catalog.Memberships = append(catalog.Memberships, Membership{
				ServiceKey: "service", Path: fmt.Sprintf("support/%03d.txt", index), Role: RoleSupporting, Origin: OriginBase,
			})
		}
		if err := Validate(catalog); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Validate() error = %v, want ErrInvalid", err)
		}
	})
	t.Run("per-service path bytes", func(t *testing.T) {
		catalog := oneServiceCatalog()
		for index := range MaxServicePaths - 1 {
			catalog.Memberships = append(catalog.Memberships, Membership{
				ServiceKey: "service", Path: fmt.Sprintf("support/%03d/%s", index, strings.Repeat("x", 510)), Role: RoleSupporting, Origin: OriginBase,
			})
		}
		if err := Validate(catalog); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Validate() error = %v, want ErrInvalid", err)
		}
	})
	t.Run("canonical bytes", func(t *testing.T) {
		catalog := largeCanonicalCatalog()
		if _, err := Canonical(catalog); !errors.Is(err, ErrCanonicalLimit) {
			t.Fatalf("Canonical() error = %v, want ErrCanonicalLimit", err)
		}
	})
	t.Run("successor edges", func(t *testing.T) {
		catalog := neutralCatalog()
		catalog.Services[1].Successors = make([]string, MaxSuccessorEdges+1)
		if err := Validate(catalog); !errors.Is(err, ErrSuccessorLimit) {
			t.Fatalf("Validate() error = %v, want ErrSuccessorLimit", err)
		}
	})
}

func TestMaximumShapeCatalog(t *testing.T) {
	if testing.Short() {
		t.Skip("maximum admitted shape")
	}

	catalog := maximumShapeCatalog()
	if len(catalog.Services) != MaxServices || len(catalog.Memberships) != MaxMemberships || len(catalog.Unowned) != 3_800 {
		t.Fatalf("shape = %d services, %d memberships, %d unowned", len(catalog.Services), len(catalog.Memberships), len(catalog.Unowned))
	}
	distinctPaths := make(map[string]struct{}, MaxDistinctPaths)
	sharedFanout := make(map[string]map[string]struct{})
	for _, membership := range catalog.Memberships {
		distinctPaths[membership.Path] = struct{}{}
		if membership.Role == RoleShared {
			services := sharedFanout[membership.Path]
			if services == nil {
				services = make(map[string]struct{})
				sharedFanout[membership.Path] = services
			}
			services[membership.ServiceKey] = struct{}{}
		}
	}
	for _, unowned := range catalog.Unowned {
		distinctPaths[unowned.Path] = struct{}{}
	}
	if len(distinctPaths) != MaxDistinctPaths {
		t.Fatalf("distinct paths = %d, want %d", len(distinctPaths), MaxDistinctPaths)
	}
	for path, services := range sharedFanout {
		if len(services) != MaxAcceptedPathFanout {
			t.Fatalf("shared fanout for %q = %d, want %d", path, len(services), MaxAcceptedPathFanout)
		}
	}
	canonical, err := Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) > MaxCanonicalBytes {
		t.Fatalf("canonical bytes = %d", len(canonical))
	}
	if cap(canonical) > MaxCanonicalBytes {
		t.Fatalf("canonical capacity = %d, want at most %d", cap(canonical), MaxCanonicalBytes)
	}
	decoded, err := Decode(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Services) != MaxServices || len(decoded.Memberships) != MaxMemberships {
		t.Fatal("maximum shape changed during decode")
	}
	if _, err := Digest(decoded); err != nil {
		t.Fatal(err)
	}
}

func neutralCatalog() Catalog {
	return Catalog{
		Schema: Schema,
		Authority: Authority{
			Kind: AuthorityCommitted, ID: "catalog.json",
			Version: "0123456789abcdef0123456789abcdef01234567",
		},
		Override: &OperatorOverride{ID: "operator", Version: "v2.1"},
		Services: []Service{
			{Key: "orders", DisplayName: "Orders", Disposition: DispositionAccepted, Origin: OriginBase},
			{Key: "legacy", DisplayName: "Legacy", Disposition: DispositionRejected, Origin: OriginOverride, Reason: "replaced", Successors: []string{"orders", "payments"}},
			{Key: "suggested", DisplayName: "Suggested", Disposition: DispositionProposal, Origin: OriginBase, Reason: "candidate only"},
			{Key: "payments", DisplayName: "Payments", Disposition: DispositionAccepted, Origin: OriginOverride},
			{Key: "ambiguous", DisplayName: "Ambiguous", Disposition: DispositionConflict, Origin: OriginBase, Reason: "two claims"},
		},
		Memberships: []Membership{
			{ServiceKey: "orders", Path: "services/orders/cmd", Role: RolePrimary, Origin: OriginBase},
			{ServiceKey: "orders", Path: "gen/orders/index.scip", Role: RoleSupporting, Origin: OriginBase},
			{ServiceKey: "orders", Path: "gen/orders/index.scip", Role: RoleTyped, Origin: OriginBase},
			{ServiceKey: "orders", Path: "shared/logging.go", Role: RoleShared, Origin: OriginBase},
			{ServiceKey: "payments", Path: "services/payments/cmd", Role: RolePrimary, Origin: OriginOverride},
			{ServiceKey: "payments", Path: "gen/payments/client.go", Role: RoleGenerated, Origin: OriginOverride},
			{ServiceKey: "payments", Path: "shared/logging.go", Role: RoleShared, Origin: OriginBase},
			{ServiceKey: "suggested", Path: "proposals/suggested", Role: RolePrimary, Origin: OriginBase},
		},
		Unowned: []UnownedPlacement{
			{Path: "tools/release.go", Origin: OriginOverride},
			{Path: "docs/notes.md", Origin: OriginBase},
		},
	}
}

func oneServiceCatalog() Catalog {
	return Catalog{
		Schema:      Schema,
		Authority:   Authority{Kind: AuthorityOperator, ID: "owner", Version: "v1"},
		Services:    []Service{{Key: "service", DisplayName: "Service", Disposition: DispositionAccepted, Origin: OriginBase}},
		Memberships: []Membership{{ServiceKey: "service", Path: "service/main.go", Role: RolePrimary, Origin: OriginBase}},
		Unowned:     []UnownedPlacement{},
	}
}

func fanoutCatalog(count int) Catalog {
	catalog := Catalog{
		Schema:    Schema,
		Authority: Authority{Kind: AuthorityOperator, ID: "owner", Version: "v1"},
		Services:  make([]Service, 0, count), Memberships: make([]Membership, 0, count*2), Unowned: []UnownedPlacement{},
	}
	for index := range count {
		key := fmt.Sprintf("service-%03d", index)
		catalog.Services = append(catalog.Services, Service{Key: key, DisplayName: key, Disposition: DispositionAccepted, Origin: OriginBase})
		catalog.Memberships = append(catalog.Memberships,
			Membership{ServiceKey: key, Path: fmt.Sprintf("services/%03d", index), Role: RolePrimary, Origin: OriginBase},
			Membership{ServiceKey: key, Path: "shared/all.go", Role: RoleShared, Origin: OriginBase},
		)
	}
	return catalog
}

func maximumShapeCatalog() Catalog {
	catalog := Catalog{
		Schema:    Schema,
		Authority: Authority{Kind: AuthorityOperator, ID: "neutral-author", Version: "max-shape-v1"},
		Services:  make([]Service, 0, MaxServices), Memberships: make([]Membership, 0, MaxMemberships),
		Unowned: make([]UnownedPlacement, 0, 3_800),
	}
	for index := range MaxServices {
		key := fmt.Sprintf("service-%04d", index)
		primary := fmt.Sprintf("services/%04d/main.go", index)
		typed := fmt.Sprintf("generated/%04d/index.scip", index)
		shared := fmt.Sprintf("shared/%03d/common.go", index/MaxAcceptedPathFanout)
		catalog.Services = append(catalog.Services, Service{Key: key, DisplayName: key, Disposition: DispositionAccepted, Origin: OriginBase})
		catalog.Memberships = append(catalog.Memberships,
			Membership{ServiceKey: key, Path: primary, Role: RolePrimary, Origin: OriginBase},
			Membership{ServiceKey: key, Path: typed, Role: RoleSupporting, Origin: OriginBase},
			Membership{ServiceKey: key, Path: typed, Role: RoleTyped, Origin: OriginBase},
			Membership{ServiceKey: key, Path: typed, Role: RoleGenerated, Origin: OriginBase},
			Membership{ServiceKey: key, Path: shared, Role: RoleShared, Origin: OriginBase},
		)
	}
	for index := range 3_800 {
		catalog.Unowned = append(catalog.Unowned, UnownedPlacement{Path: fmt.Sprintf("unowned/%04d/file.go", index), Origin: OriginBase})
	}
	return catalog
}

func largeCanonicalCatalog() Catalog {
	catalog := Catalog{
		Schema:    Schema,
		Authority: Authority{Kind: AuthorityOperator, ID: "owner", Version: "v1"},
		Services:  make([]Service, 0, MaxServices), Memberships: make([]Membership, 0, MaxMemberships), Unowned: []UnownedPlacement{},
	}
	for serviceIndex := range MaxServices {
		key := fmt.Sprintf("service-%04d", serviceIndex)
		catalog.Services = append(catalog.Services, Service{Key: key, DisplayName: key, Disposition: DispositionAccepted, Origin: OriginBase})
		primaryPath := fmt.Sprintf("primary/%04d/%s", serviceIndex, strings.Repeat("p", 280))
		sharedPath := fmt.Sprintf("support/%04d/%s", serviceIndex, strings.Repeat("s", 280))
		for _, membership := range []struct {
			path string
			role string
		}{
			{path: primaryPath, role: RolePrimary},
			{path: sharedPath, role: RoleSupporting},
			{path: sharedPath, role: RoleGenerated},
			{path: sharedPath, role: RoleShared},
			{path: sharedPath, role: RoleTyped},
		} {
			catalog.Memberships = append(catalog.Memberships, Membership{
				ServiceKey: key,
				Path:       membership.path,
				Role:       membership.role, Origin: OriginBase,
			})
		}
	}
	return catalog
}

func replaceOnce(t *testing.T, content []byte, old, replacement string) []byte {
	t.Helper()
	if !bytes.Contains(content, []byte(old)) {
		t.Fatalf("canonical fixture does not contain %q", old)
	}
	return bytes.Replace(content, []byte(old), []byte(replacement), 1)
}

func withoutUnowned(t *testing.T, content []byte) []byte {
	t.Helper()
	index := bytes.Index(content, []byte(`,"unowned":`))
	if index < 0 {
		t.Fatal("canonical fixture has no unowned field")
	}
	result := slices.Clone(content[:index])
	return append(result, '}')
}

func collectionInput(field, item string, count int) []byte {
	collections := map[string]string{"services": `[]`, "memberships": `[]`, "unowned": `[]`}
	collections[field] = `[` + strings.Repeat(item+`,`, count-1) + item + `]`
	value := map[string]json.RawMessage{
		"schema":      json.RawMessage(`"phebs-service-catalog-v2"`),
		"authority":   json.RawMessage(`{"kind":"operator","id":"owner","version":"v1"}`),
		"services":    json.RawMessage(collections["services"]),
		"memberships": json.RawMessage(collections["memberships"]),
		"unowned":     json.RawMessage(collections["unowned"]),
	}
	content, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return content
}
