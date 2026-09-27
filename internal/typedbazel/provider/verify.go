package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
)

type flatResponse struct {
	Roots    []string
	Packages []struct {
		ID, Name, PkgPath, ExportFile string
		CompiledGoFiles               []string
	}
}

func verifyPreparedCall(p launcher.Prepared, slot string, call CallEvidence) error {
	if call.Slot != slot || call.RequestSHA256 != hash(call.Request) || call.LauncherSHA256 != p.Digest() || !reflect.DeepEqual(call.Launcher, p.Invocation()) || call.DriverResponseSHA256 != hash(call.Result.DriverResponse) || call.ResponseSHA256 != hash(call.Result.Response) {
		return errors.New("client call evidence identity mismatch")
	}
	if err := launcher.Reconcile(p, call.Result.DriverResponse); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(call.Result.DriverResponse, &raw); err != nil {
		return err
	}
	raw["Compiler"], raw["Arch"], raw["GoVersion"] = json.RawMessage(`"gc"`), json.RawMessage(`"arm64"`), json.RawMessage(`25`)
	expected, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, call.Result.Response) {
		return errors.New("typed response differs from exact metadata adaptation")
	}
	var exports struct{ Packages []struct{ ExportFile string } }
	if err = json.Unmarshal(call.Result.DriverResponse, &exports); err != nil {
		return err
	}
	paths := map[string]bool{}
	for _, p := range exports.Packages {
		if p.ExportFile != "" {
			paths[p.ExportFile] = true
		}
	}
	ordered := make([]string, 0, len(paths))
	for name := range paths {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)
	if len(ordered) != len(call.Result.Exports) {
		return errors.New("export evidence inventory mismatch")
	}
	total := 0
	for i, export := range call.Result.Exports {
		if export.Path != ordered[i] || export.Bytes <= 0 || export.Bytes > planner.MaxFileBytes || export.Bytes > planner.MaxProtoBytes-total || !digest("sha256:"+export.SHA256) {
			return errors.New("export evidence identity mismatch")
		}
		total += export.Bytes
	}

	return nil
}

type loadFact struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Types    bool   `json:"types"`
	Syntax   int    `json:"syntax"`
	TypeInfo bool   `json:"type_info"`
	Errors   int    `json:"errors"`
	IllTyped bool   `json:"ill_typed"`
}
type loadReport struct {
	Mode     int        `json:"mode"`
	Roots    []loadFact `json:"roots"`
	Packages []loadFact `json:"packages"`
}

func verifyNativeProbe(data, response []byte) error {
	report, err := decode[loadReport](data, maxClientBytes)
	if err != nil {
		return err
	}
	var flat flatResponse
	if err = json.Unmarshal(response, &flat); err != nil {
		return err
	}
	if report.Mode != launcher.CompatibilityMode || len(report.Roots) != len(flat.Roots) || len(report.Roots) == 0 || len(report.Packages) != len(flat.Packages) || len(flat.Packages) > 8192 {
		return errors.New("native probe closure mismatch")
	}
	want := map[string]loadFact{}
	roots := map[string]bool{}
	for _, id := range flat.Roots {
		if roots[id] {
			return errors.New("native duplicate root")
		}
		roots[id] = true
	}
	for _, p := range flat.Packages {
		if _, ok := want[p.ID]; ok {
			return errors.New("native duplicate package")
		}
		want[p.ID] = loadFact{ID: p.ID, Path: p.PkgPath, Syntax: len(p.CompiledGoFiles)}
	}
	observed := map[string]loadFact{}
	previous := ""
	for _, p := range report.Packages {
		w, ok := want[p.ID]
		if !ok || p.ID <= previous || p.Path != w.Path || !p.Types || p.Errors != 0 || p.IllTyped || p.Syntax < 0 || p.Syntax > planner.MaxDocuments || roots[p.ID] && (!p.TypeInfo || p.Syntax != w.Syntax || p.Syntax == 0) {
			return errors.New("native typed package incomplete")
		}
		previous = p.ID
		observed[p.ID] = p
	}
	previous = ""
	for _, p := range report.Roots {
		if !roots[p.ID] || p.ID <= previous || observed[p.ID] != p || !p.TypeInfo || p.Syntax == 0 {
			return errors.New("native typed root mismatch")
		}
		previous = p.ID
	}
	return nil
}
