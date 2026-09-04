// Package t421catalogprojection derives the five exact logical set identities
// shared by T42.1 plan authoring and final exact inspection.
package t421catalogprojection

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"slices"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

// SetIdentity is the scalar identity of one domain-framed logical set.
type SetIdentity struct {
	Records     uint64 `json:"records"`
	FramedBytes uint64 `json:"framed_bytes"`
	SHA256      string `json:"sha256"`
}

// Projection contains the five exact logical set identities.
type Projection struct {
	Catalog         SetIdentity
	Memberships     SetIdentity
	Placements      SetIdentity
	UnownedPrefixes SetIdentity
	ServiceQueries  SetIdentity
}

type role struct {
	Role   string `json:"role"`
	Origin string `json:"origin"`
}

type claim struct {
	ServiceKey  string `json:"service_key"`
	Disposition string `json:"disposition"`
	Roles       []role `json:"roles"`
}

type placement struct {
	Path          string  `json:"path"`
	Unowned       bool    `json:"unowned"`
	UnownedOrigin string  `json:"unowned_origin,omitempty"`
	Claims        []claim `json:"claims"`
}

type queryPath struct {
	Path  string `json:"path"`
	Roles []role `json:"roles"`
}

type serviceQuery struct {
	ServiceKey string      `json:"service_key"`
	Paths      []queryPath `json:"paths"`
}

// Derive computes the exact T42.1 identities without mutating catalog.
func Derive(ctx context.Context, catalog servicecatalog.Catalog) (Projection, error) {
	if ctx == nil {
		return Projection{}, errors.New("T42.1 catalog projection requires context")
	}
	if err := ctx.Err(); err != nil {
		return Projection{}, err
	}
	services := slices.Clone(catalog.Services)
	slices.SortFunc(services, func(left, right servicecatalog.Service) int {
		return compare(left.Key, right.Key)
	})
	memberships := slices.Clone(catalog.Memberships)
	slices.SortFunc(memberships, compareMemberships)
	unowned := slices.Clone(catalog.Unowned)
	slices.SortFunc(unowned, func(left, right servicecatalog.UnownedPlacement) int {
		return compare(left.Path, right.Path)
	})

	catalogIdentity := newIdentityBuilder("t421-independent-catalog-v1")
	membershipIdentity := newIdentityBuilder("t421-independent-memberships-v1")
	placementIdentity := newIdentityBuilder("t421-independent-placements-v1")
	unownedPrefixIdentity := newIdentityBuilder("t421-independent-unowned-prefixes-v1")
	queryIdentity := newIdentityBuilder("t421-independent-service-queries-v1")
	dispositions := make(map[string]string, len(services))
	for _, service := range services {
		if err := add(ctx, catalogIdentity, service); err != nil {
			return Projection{}, err
		}
		dispositions[service.Key] = service.Disposition
	}
	for _, membership := range memberships {
		if err := add(ctx, membershipIdentity, membership); err != nil {
			return Projection{}, err
		}
	}

	queries := make(map[string][]queryPath, len(services))
	placements := make(map[string]*placement, len(memberships)+len(unowned))
	for _, membership := range memberships {
		if err := ctx.Err(); err != nil {
			return Projection{}, err
		}
		value := placements[membership.Path]
		if value == nil {
			value = &placement{Path: membership.Path, Claims: []claim{}}
			placements[membership.Path] = value
		}
		claimIndex := len(value.Claims) - 1
		if claimIndex < 0 || value.Claims[claimIndex].ServiceKey != membership.ServiceKey {
			value.Claims = append(value.Claims, claim{
				ServiceKey: membership.ServiceKey, Disposition: dispositions[membership.ServiceKey],
				Roles: []role{},
			})
			claimIndex = len(value.Claims) - 1
		}
		memberRole := role{Role: membership.Role, Origin: membership.Origin}
		value.Claims[claimIndex].Roles = append(value.Claims[claimIndex].Roles, memberRole)

		paths := queries[membership.ServiceKey]
		if len(paths) == 0 || paths[len(paths)-1].Path != membership.Path {
			paths = append(paths, queryPath{Path: membership.Path})
		}
		paths[len(paths)-1].Roles = append(paths[len(paths)-1].Roles, memberRole)
		queries[membership.ServiceKey] = paths
	}
	for _, value := range unowned {
		if err := ctx.Err(); err != nil {
			return Projection{}, err
		}
		if placements[value.Path] != nil {
			return Projection{}, errors.New("T42.1 catalog path is both owned and unowned")
		}
		placements[value.Path] = &placement{
			Path: value.Path, Unowned: true, UnownedOrigin: value.Origin, Claims: []claim{},
		}
		if value.Origin == servicecatalog.OriginOverride {
			if err := unownedPrefixIdentity.add(value); err != nil {
				return Projection{}, err
			}
		}
	}
	paths := make([]string, 0, len(placements))
	for path := range placements {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		if err := add(ctx, placementIdentity, *placements[path]); err != nil {
			return Projection{}, err
		}
	}
	for _, service := range services {
		if err := add(ctx, queryIdentity, serviceQuery{
			ServiceKey: service.Key, Paths: queries[service.Key],
		}); err != nil {
			return Projection{}, err
		}
	}
	return Projection{
		Catalog: catalogIdentity.finish(), Memberships: membershipIdentity.finish(),
		Placements: placementIdentity.finish(), UnownedPrefixes: unownedPrefixIdentity.finish(),
		ServiceQueries: queryIdentity.finish(),
	}, nil
}

func add(ctx context.Context, builder *identityBuilder, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return builder.add(value)
}

func compareMemberships(left, right servicecatalog.Membership) int {
	for _, pair := range [][2]string{
		{left.ServiceKey, right.ServiceKey}, {left.Path, right.Path},
		{left.Role, right.Role}, {left.Origin, right.Origin},
	} {
		if result := compare(pair[0], pair[1]); result != 0 {
			return result
		}
	}
	return 0
}

func compare(left, right string) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

type identityBuilder struct {
	hash    hash.Hash
	bytes   uint64
	records uint64
}

func newIdentityBuilder(domain string) *identityBuilder {
	builder := &identityBuilder{hash: sha256.New()}
	builder.writeFrame([]byte(domain))
	return builder
}

func (builder *identityBuilder) add(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	builder.writeFrame(raw)
	builder.records++
	return nil
}

func (builder *identityBuilder) writeFrame(raw []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(raw)))
	_, _ = builder.hash.Write(length[:])
	_, _ = builder.hash.Write(raw)
	builder.bytes += uint64(len(length) + len(raw))
}

func (builder *identityBuilder) finish() SetIdentity {
	return SetIdentity{
		Records: builder.records, FramedBytes: builder.bytes,
		SHA256: "sha256:" + hex.EncodeToString(builder.hash.Sum(nil)),
	}
}
