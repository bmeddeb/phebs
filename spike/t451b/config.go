package t451b

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t451a"
)

// Config names existing operator-prepared objects; it cannot select a workload,
// command, environment, repository, cohort, driver or container recipe.
type Config struct {
	Socket   string  `json:"socket"`
	Parent   string  `json:"parent"`
	Helper   string  `json:"helper"`
	Bundle   string  `json:"bundle"`
	Manifest string  `json:"manifest"`
	Receipt  string  `json:"receipt"`
	Request  Request `json:"request"`
}

func ReadConfig(name string) (Config, error) {
	data, err := readBounded(name, 16384)
	if err != nil {
		return Config{}, err
	}
	c, err := decode[Config](data, 16384, true)
	if err != nil {
		return c, err
	}
	for _, p := range []string{c.Socket, c.Parent, c.Helper, c.Bundle, c.Manifest, c.Receipt} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" {
			return c, errors.New("native config requires exact absolute paths")
		}
	}
	raw, _ := json.MarshalIndent(c.Request, "", "  ")
	_, err = DecodeRequest(append(raw, '\n'))
	return c, err
}
func RunConfig(ctx context.Context, c Config) (Receipt, error) {
	manifest, err := t451a.ReadManifest(c.Manifest)
	if err != nil {
		return Receipt{}, err
	}
	// Reserve output before native execution; never run if evidence would replace
	// an earlier attempt or cannot be retained under this exact operator path.
	file, err := os.OpenFile(c.Receipt, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Receipt{}, err
	}
	receipt, runErr := Run(ctx, Options{Socket: c.Socket, Parent: c.Parent, Helper: c.Helper, BundleRoot: c.Bundle, Manifest: manifest, Request: c.Request})
	encodeErr := json.NewEncoder(file).Encode(receipt)
	return receipt, errors.Join(runErr, encodeErr, file.Close())
}
