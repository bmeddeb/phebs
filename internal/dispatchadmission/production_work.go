package dispatchadmission

// ProductionWorkSelected is separate from server semantic selection. The two
// closed offline archive lifetimes require work coverage without acquiring
// server owners, request windows, lifecycle scheduling or semantic stdin.
// Selection stays true after failure so callers cannot fall back to no-op.
func ProductionWorkSelected() bool {
	lifetime := productionRuntime.Load()
	return lifetime != nil && (lifetime.semanticMode != "" || lifetime.program == ProgramPhebs &&
		(lifetime.producerID == 10 || lifetime.producerID == 11))
}

// ProductionWorkState returns copied bootstrap identity and actual local phase.
// Empty Mode identifies the closed offline profile; it makes no owner-drainage
// or readiness assertion. Server semantics retain their existing stricter inlet.
func ProductionWorkState() (ProductionSemanticSnapshot, error) {
	lifetime := productionRuntime.Load()
	if lifetime != nil && lifetime.semanticMode != "" {
		return ProductionSemanticState()
	}
	if lifetime == nil || lifetime.program != ProgramPhebs ||
		(lifetime.producerID != 10 && lifetime.producerID != 11) || lifetime.inputSHA256 == ([32]byte{}) ||
		lifetime.client == nil || lifetime.storeClient == nil {
		return ProductionSemanticSnapshot{}, ErrProductionBootstrap
	}
	if _, err := ProcessStoreOwner(); err != nil {
		return ProductionSemanticSnapshot{}, err
	}
	client := lifetime.client
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed || client.err != nil || client.ctx.Err() != nil || client.ownersRequired || client.phase != 12 {
		return ProductionSemanticSnapshot{}, ErrProductionBootstrap
	}
	return ProductionSemanticSnapshot{InputSHA256: lifetime.inputSHA256, ProducerID: lifetime.producerID, Phase: client.phase}, nil
}

// RequireProductionWorkCommand binds the offline producer to its real command
// before configuration or native work. Ordinary and semantic server routing is
// unchanged. The trusted parent still owns executable and input admission.
func RequireProductionWorkCommand(command string) error {
	lifetime := productionRuntime.Load()
	if lifetime == nil || lifetime.semanticMode != "" || lifetime.program != ProgramPhebs ||
		(lifetime.producerID != 10 && lifetime.producerID != 11) {
		return nil
	}
	if _, err := ProductionWorkState(); err != nil {
		return err
	}
	if lifetime.producerID == 10 && command == "backup" || lifetime.producerID == 11 && command == "restore" {
		return nil
	}
	return lifetime.client.fail(ErrProductionBootstrap)
}
