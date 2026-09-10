//go:build darwin || linux

package dispatchadmission

import "testing"

func TestProductionOfflineWorkRefusalSticky(t *testing.T) {
	for _, producer := range []uint32{10, 11} {
		_, client, server := paired(t, testConfig())
		setPipedTestRuntime(t, &ProductionLifetime{program: ProgramPhebs, producerID: producer, inputSHA256: [32]byte{1}, client: client})
		if !ProductionWorkSelected() || ProductionSemanticSelected() {
			t.Fatal("offline work enabled semantic serving")
		}
		if _, err := ProductionSemanticState(); err == nil {
			t.Fatal("offline lifetime admitted server state")
		}
		if _, err := ProductionWorkState(); err == nil {
			t.Fatal("missing actual store owner admitted")
		}
		if err := ObserveProductionSourceRead(t.Context()); err == nil || client.Context().Err() == nil || !ProductionWorkSelected() {
			t.Fatal("missing offline coverage did not latch")
		}
		if err := <-server; err == nil {
			t.Fatal("failed selected transport remained healthy")
		}
	}
}
