package store

import (
	"context"
	"errors"

	"github.com/fxamacker/cbor/v2"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

type startupWriterMarkerRec struct {
	ID      models.RecordID `json:"id"`
	Version cbor.RawMessage `json:"version"`
	Missing *bool           `json:"missing"`
}

const startupWriterMarkerSQL = "SELECT id, version, (version = NONE) AS missing FROM $marker LIMIT 1;"

// The three source-owned writer migrations share only strict marker decoding.
// Each still owns its version policy, affected-row predicates and native fence.
// The one-row result bound is not a pre-decode bound on a corrupted version.
func (s *Surreal) startupWriterMarker(ctx context.Context, marker models.RecordID) (string, error) {
	results, err := storeQuery[[]startupWriterMarkerRec](ctx, s.accounting, s.db,
		startupWriterMarkerSQL, map[string]any{"marker": marker}, storeRead())
	if err != nil {
		return "", err
	}
	if results == nil || len(*results) != 1 || (*results)[0].Status != "OK" || (*results)[0].Error != nil ||
		(*results)[0].Result == nil || len((*results)[0].Result) > 1 {
		return "", errors.New("invalid startup writer marker result")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	rows := (*results)[0].Result
	if len(rows) == 0 {
		return "", nil
	}
	row := rows[0]
	id, idOK := row.ID.ID.(string)
	wantID, wantOK := marker.ID.(string)
	if !idOK || !wantOK || marker.Table != "store_migration" || row.ID.Table != marker.Table || id != wantID || row.Missing == nil {
		return "", errors.New("invalid startup writer marker identity or absence witness")
	}
	if *row.Missing {
		if string(row.Version) != "\xc6\xf6" {
			return "", errors.New("startup writer marker absence witness differs")
		}
		return "", nil
	}
	var version string
	if len(row.Version) == 0 || row.Version[0]>>5 != 3 || cbor.Unmarshal(row.Version, &version) != nil || version == "" {
		return "", errors.New("invalid startup writer marker version")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return version, nil
}

func startupWriterExpected(version string) any {
	if version == "" {
		return models.None
	}
	return version
}

func (s *Surreal) verifyStartupWriterMarker(ctx context.Context, marker models.RecordID, wanted string) error {
	actual, err := s.startupWriterMarker(ctx, marker)
	if err != nil {
		return err
	}
	if actual != wanted {
		return errors.New("startup writer completion marker differs")
	}
	return nil
}
