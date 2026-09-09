package main

import (
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/indexer"
)

func TestT422IndexReports(t *testing.T) {
	state := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	for _, test := range []struct {
		event indexer.IndexOfferEvent
		want  string
	}{
		{indexer.IndexOfferEvent{Kind: 'b'}, "Ib2\n"},
		{indexer.IndexOfferEvent{Kind: 'i', Count: 1}, "I2\n"},
		{indexer.IndexOfferEvent{Kind: 'e', Count: 513}, "Ie2:513\n"},
		{indexer.IndexOfferEvent{Kind: 'i', Count: 2}, ""},
		{indexer.IndexOfferEvent{Kind: 'x'}, ""},
	} {
		raw, err := t422IndexRecord(state, state, test.event)
		if (err == nil) != (test.want != "") || string(raw) != test.want {
			t.Fatal(string(raw), err)
		}
	}
	changed := state
	changed.InputSHA256 = [32]byte{2}
	if _, err := t422IndexRecord(changed, state, indexer.IndexOfferEvent{Kind: 'i', Count: 1}); err == nil {
		t.Fatal("changed binding admitted")
	}
	ctx, err := bindT422IndexReports(t.Context(), nil)
	if err != nil || ctx != t.Context() {
		t.Fatal("ordinary binding changed", err)
	}
}
