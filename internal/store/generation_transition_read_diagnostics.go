package store

import (
	"context"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"strings"
)

// Preserve the ordinary query byte-for-byte. Only the optional private
// observer selects extra scalar fields from the same existing transaction.
const generationTransitionReadSQL = `
BEGIN;
LET $chunk = (SELECT * FROM generation_schedule_chunk
	WHERE identity = $identity LIMIT 1)[0];
LET $current_digest = (SELECT VALUE schedule_digest FROM $current LIMIT 1)[0];
RETURN [{ chunk: $chunk, current_schedule_digest: $current_digest }];
COMMIT;`

var generationTransitionDiagnosticSQL = strings.Replace(generationTransitionReadSQL,
	`RETURN [{ chunk: $chunk, current_schedule_digest: $current_digest }];`,
	`RETURN [{ chunk: $chunk, current_schedule_digest: $current_digest,
 reader_diagnostics: true, chunk_present: $chunk != NONE,
 record_id_present: $chunk.id != NONE, record_id_is_record: type::is_record($chunk.id) }];`, 1)

func generationTransitionReadQuery(ctx context.Context) string {
	if readaccounting.HasTransitionReadFailureObserver(ctx) {
		return generationTransitionReadSQL
	}
	return generationTransitionReadSQL
}
func generationTransitionDecodedID(record generationChunkRec) readaccounting.TransitionReadIDKind {
	if record.RecID == nil {
		return readaccounting.TransitionReadIDMissing
	}
	// Match chunk()'s existing validation order: both invalid cases still
	// return the identical original error, independent of these labels.
	if _, ok := record.RecID.ID.(string); !ok {
		return readaccounting.TransitionReadIDWrongType
	}
	if record.RecID.Table != "generation_schedule_chunk" {
		return readaccounting.TransitionReadIDWrongTable
	}
	return readaccounting.TransitionReadIDValid
}
