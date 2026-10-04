package typedindex

import (
	"runtime"
	"testing"
)

func TestAdmittedNativeArch(t *testing.T) {
	if !AdmittedNativeArch("arm64") || !AdmittedNativeArch("amd64") {
		t.Fatal("admitted hosts refused")
	}
	if AdmittedNativeArch("386") || AdmittedNativeArch("") {
		t.Fatal("other architecture admitted")
	}
	arm, ok := AdmittedNativeToolTag("arm64")
	amd, ok2 := AdmittedNativeToolTag("amd64")
	if !ok || !ok2 || arm != "arm64.v8.0" || amd != "amd64.v1" || !AdmittedNativeVariant("arm64", "v8.0", "") || !AdmittedNativeVariant("amd64", "", "v1") {
		t.Fatal("pinned variant mismatch")
	}
	if AdmittedNativeVariant("amd64", "v8.0", "") || AdmittedNativeVariant("arm64", "", "v1") || AdmittedNativeVariant("386", "v8.0", "v1") {
		t.Fatal("cross-architecture variant admitted")
	}
	_, ok = AdmittedNativeToolTag("386")
	if ok {
		t.Fatal("other architecture has a tool tag")
	}
	if ReducedConfig().GOARCH != "arm64" || Amd64ReducedConfig().GOARCH != "amd64" || Amd64ReducedConfig().GOOS != ReducedConfig().GOOS {
		t.Fatal("historical reduced profile architecture changed")
	}
	if Amd64ReducedConfig() == ReducedConfig() {
		t.Fatal("amd64 successor aliases the historical profile")
	}
	schema, config := HostReducedIdentity()
	if config.GOARCH != runtime.GOARCH || (runtime.GOARCH == "amd64" && schema != Amd64ProfileSchema) || (runtime.GOARCH == "arm64" && (schema != ProfileSchema || config != ReducedConfig())) {
		t.Fatal("host reduced identity does not match this process")
	}
	if MeasuredPolicy().CPUQuotaMicros != 200000 || MeasuredPolicy().CPUPeriodMicros != 100000 {
		t.Fatal("worker CPU quota changed")
	}
	if !AdmittedNativeArch(runtime.GOARCH) {
		t.Fatal("this test host is outside the ceremony set")
	}
}
