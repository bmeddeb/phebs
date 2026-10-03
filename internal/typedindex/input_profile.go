package typedindex

// InputConfig describes the closed additional-provider recipes. Historical
// Bazel profiles retain their schemas, configuration and canonical identities.
func InputConfig(provider string) (Config, error) {
	c := ReducedConfig()
	switch provider {
	case ModuleProviderID:
		c.Mode = "go-module"
	case ImportProviderID:
		c.Mode = "artifact-import"
	default:
		return Config{}, Unsupported
	}
	return c, nil
}

func validateInputProfile(d ProfileDefinition) error {
	c, err := InputConfig(d.Provider)
	if err != nil {
		return err
	}
	if d.Config != c || d.Policy != MeasuredPolicy() || d.RCDigest != "" {
		return Unsupported
	}
	// No dormant Bazel tools may be smuggled into a non-Bazel recipe. The
	// planner and launcher are the same authenticated Phebs worker image.
	if d.Tools.Bazel != (Tool{}) || d.Tools.RulesGo != (Tool{}) || d.Tools.Driver != (Tool{}) || d.Tools.Planner != d.Tools.Launcher || !token(d.Tools.Launcher.Version) || !digest(d.Tools.Launcher.Digest) {
		return Invalid
	}
	if d.Tools.Indexer.Version != "0.2.7" || !digest(d.Tools.Indexer.Digest) {
		return Unsupported
	}
	if d.Provider == ModuleProviderID {
		if d.Tools.Go.Version != "1.25.0" || !digest(d.Tools.Go.Digest) || d.Tools.Indexer.Digest != SCIPGoIndexerDigest {
			return Unsupported
		}
	} else if d.Tools.Go != (Tool{}) {
		// Import records declared producer provenance separately. It runs no
		// Go SDK or indexer; naming one here must not imply executed identity.
		return Invalid
	}
	return nil
}

// SelectionFile is a fixed inventory member, never a caller-controlled path.
func SelectionFile(provider string) (string, error) {
	switch provider {
	case ProviderID:
		return "typed-bazel-selection.json", nil
	case ModuleProviderID:
		return "typed-module-selection.json", nil
	case ImportProviderID:
		return "typed-import-selection.json", nil
	default:
		return "", Unsupported
	}
}
