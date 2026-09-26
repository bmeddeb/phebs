package t451b

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
)

func TestNativeScratchSelection(t *testing.T) {
	old, err := json.MarshalIndent(testRequest(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	old = append(old, '\n')
	native := nativeRequestBytes(validNativeRequest(t))
	if NativeProfile != "native-linux-arm64-rules-go-059-v2" || sandbox.ScratchInodes != 65536 || sandbox.NativeT451bScratchInodes != 262144 {
		t.Fatal("prospective native profile or old bound changed")
	}
	for _, tc := range []struct {
		data   []byte
		native bool
	}{{old, false}, {native, true}} {
		got, err := nativeSandboxRequest(tc.data)
		if err != nil || got != tc.native {
			t.Fatal("closed profile selection", got, err)
		}
	}
	for _, raw := range [][]byte{
		bytes.Replace(native, []byte(NativeProfile), []byte("native-linux-arm64-rules-go-059-v1"), 1),
		bytes.Replace(native, []byte(NativeProfile), []byte("unknown"), 1),
		bytes.Replace(native, []byte(`"schema":`), []byte(`"scratch_inodes":262144,"schema":`), 1),
		bytes.Replace(native, []byte(`"schema":`), []byte(`"schema":"phebs-t451b-request-v1","schema":`), 1),
		bytes.Replace(old, []byte(Profile), []byte(NativeProfile), 1),
		[]byte(`{"schema":"unknown"}`),
	} {
		if got, err := nativeSandboxRequest(raw); err == nil || got {
			t.Fatal("unknown or mixed request acquired native cap")
		}
	}
	if _, err = DecodeRequest(native); err == nil {
		t.Fatal("original worker accepted native request")
	}
	if _, err = DecodeNativeRequest(old); err == nil {
		t.Fatal("native worker accepted original request")
	}
	if _, _, err = ReadWorkerProfile(); err == nil {
		t.Fatal("host entered original worker")
	}
	if _, err = NativeWorkerRequest(); err == nil {
		t.Fatal("host entered native worker")
	}
}
