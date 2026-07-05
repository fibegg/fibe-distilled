package service

// ApplyServiceOverrides mutates rendered Compose with SDK service overrides.
func ApplyServiceOverrides(rendered map[string]any, overrides map[string]any) error {
	services, ok := AsMap(rendered["services"])
	if !ok {
		return nil
	}
	for serviceName, rawOverride := range overrides {
		if err := applyServiceOverrideByName(services, serviceName, rawOverride); err != nil {
			return err
		}
	}
	return nil
}

// applyServiceOverrideByName dispatches global and named service override buckets.
func applyServiceOverrideByName(services map[string]any, serviceName string, rawOverride any) error {
	if serviceName == globalServiceOverrideKey {
		return applyGlobalServiceOverride(services, rawOverride)
	}
	return applyNamedServiceOverride(services, serviceName, rawOverride)
}

// applyGlobalServiceOverride handles the _global env override bucket.
func applyGlobalServiceOverride(services map[string]any, rawOverride any) error {
	override, ok := AsMap(rawOverride)
	if !ok {
		return nil
	}
	if envVars, ok := AsMap(override["env_vars"]); ok {
		return applyGlobalEnvAny(services, envVars)
	}
	return nil
}

// applyNamedServiceOverride applies overrides to one service if present.
func applyNamedServiceOverride(services map[string]any, serviceName string, rawOverride any) error {
	serviceMap, ok := AsMap(services[serviceName])
	if !ok {
		return nil
	}
	override, ok := AsMap(rawOverride)
	if !ok {
		return nil
	}
	return applyServiceOverride(serviceName, serviceMap, override)
}

// applyServiceOverride applies env, label, and runtime service mutations.
func applyServiceOverride(serviceName string, serviceMap map[string]any, override map[string]any) error {
	if err := applyServiceEnvOverride(serviceName, serviceMap, override); err != nil {
		return err
	}
	applyServiceLabelOverrides(serviceMap, override)
	applyServiceRuntimeOverrides(serviceMap, override)
	return nil
}
