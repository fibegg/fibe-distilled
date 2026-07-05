package service

// applyServiceEnvOverride merges env_vars into a service environment map.
func applyServiceEnvOverride(serviceName string, serviceMap map[string]any, override map[string]any) error {
	if envVars, ok := AsMap(override["env_vars"]); ok {
		return applyEnvOverrideValues(serviceName, serviceMap, envVars)
	}
	return nil
}

// applyServiceRuntimeOverrides applies command, ports, and image overrides.
func applyServiceRuntimeOverrides(serviceMap map[string]any, override map[string]any) {
	if mappings := normalizePortMappings(override["port_mappings"]); len(mappings) > 0 {
		serviceMap["ports"] = applyPortMappings(serviceMap["ports"], mappings)
	}
	if command, ok := normalizeStartCommand(override["start_command"]); ok {
		serviceMap["command"] = command
	}
	if value := overrideString(override, "image"); value != "" {
		serviceMap["image"] = value
	}
}
