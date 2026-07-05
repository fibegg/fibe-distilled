package service

import (
	"errors"
	"fmt"
)

var (
	// errSubdomainPathNotTraversable marks missing or malformed service label paths.
	errSubdomainPathNotTraversable = errors.New("path was not found or is not traversable")
	// errBlankSubdomainValue marks blank service_subdomains entries.
	errBlankSubdomainValue = errors.New("value must not be blank")
)

// ApplyServiceSubdomains writes launch-time subdomain overrides into labels.
func ApplyServiceSubdomains(rendered map[string]any, subdomains map[string]string) error {
	if len(subdomains) == 0 {
		return nil
	}
	services, ok := AsMap(rendered["services"])
	if !ok {
		return errSubdomainPathNotTraversable
	}
	for serviceName, subdomain := range subdomains {
		if err := applyServiceSubdomain(services, serviceName, subdomain); err != nil {
			return err
		}
	}
	return nil
}

// applyServiceSubdomain writes one service subdomain into normalized labels.
func applyServiceSubdomain(services map[string]any, serviceName string, subdomain string) error {
	if issue := invalidSubdomainOverrideIssue(serviceName, subdomain); issue != "" {
		return fmt.Errorf("%s could not be written: %w", issue, errBlankSubdomainValue)
	}
	serviceMap, ok := AsMap(services[serviceName])
	if !ok {
		return fmt.Errorf("service_subdomains.%s could not be written: %w", serviceName, errSubdomainPathNotTraversable)
	}
	labels, err := mutableServiceLabels(serviceMap)
	if err != nil {
		return fmt.Errorf("service_subdomains.%s could not be written: %w", serviceName, err)
	}
	labels["fibe.gg/subdomain"] = subdomain
	serviceMap["labels"] = labels
	services[serviceName] = serviceMap
	return nil
}

// mutableServiceLabels returns mutable map-form labels from supported Compose shapes.
func mutableServiceLabels(serviceMap map[string]any) (map[string]any, error) {
	raw, exists := serviceMap["labels"]
	if !exists || raw == nil {
		return map[string]any{}, nil
	}
	switch raw.(type) {
	case map[string]any, map[any]any, []any, []string:
		return normalizeLabelsAny(raw), nil
	default:
		return nil, errSubdomainPathNotTraversable
	}
}
