package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"testing"
)

func TestT422LogicalStoreWorkLaunchPolicy(t *testing.T) {
	for _, mode := range []string{"omitted", "enabled", "unknown", "epoch", "producer", "phase", "no_cleanup", "unbound"} {
		t.Run(mode, func(t *testing.T) {
			raw, snapshot := t422SemanticTestRequest(t)
			var request t422SemanticLaunchRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			request.ServerEpoch, snapshot.ProducerID, snapshot.Phase = 2, 3, 5
			request.SelectorHandoffCleanup = t422SelectorCleanupSchema
			if mode != "omitted" {
				request.LogicalStoreWork = t422LogicalStoreWorkSchema
			}
			switch mode {
			case "unknown":
				request.LogicalStoreWork = "unknown"
			case "epoch":
				request.ServerEpoch, snapshot.Phase = 1, 2
			case "producer":
				snapshot.ProducerID = 2
			case "phase":
				snapshot.Phase = 6
			case "no_cleanup":
				request.SelectorHandoffCleanup = ""
			}
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, '\n')
			snapshot.InputSHA256 = sha256.Sum256(raw)
			if mode == "unbound" {
				snapshot.InputSHA256[0] ^= 1
			}
			launch, err := decodeT422SemanticLaunch(raw, snapshot)
			if (err == nil) != (mode == "enabled" || mode == "omitted") {
				t.Fatal("policy admission differs", err)
			}
			if mode == "omitted" && (bytes.Contains(raw, []byte("logical_store_work")) || launch.request.LogicalStoreWork != "") {
				t.Fatal("omitted policy gained bytes or behavior")
			}
		})
	}
}
