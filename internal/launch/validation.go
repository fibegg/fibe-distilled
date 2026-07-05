package launch

import (
	"net/http"
	"sort"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/api/request"
)

// validateLaunchPayload validates launch identity, booleans, objects, and subdomain overrides.
func validateLaunchPayload(body launchPayload) error {
	if err := validateLaunchIdentityFields(body); err != nil {
		return err
	}
	if err := validateLaunchBooleanFields(body); err != nil {
		return err
	}
	if err := validateLaunchObjectFields(body); err != nil {
		return err
	}
	return validateLaunchSubdomainFields(body)
}

// validateLaunchIdentityFields validates required launch identity fields.
func validateLaunchIdentityFields(body launchPayload) error {
	for _, check := range []func(launchPayload) error{
		validateLaunchComposeYAMLField,
		validateLaunchNameField,
		validateLaunchRepositoryField,
		validateLaunchMarqueeField,
	} {
		if err := check(body); err != nil {
			return err
		}
	}
	return nil
}

// validateLaunchComposeYAMLField checks the required Compose payload.
func validateLaunchComposeYAMLField(body launchPayload) error {
	if strings.TrimSpace(body.ComposeYAML) == "" {
		if body.fields.Has("compose_yaml") {
			return badRequestError{message: "compose_yaml must not be blank"}
		}
		return badRequestError{message: "compose_yaml is required"}
	}
	return nil
}

// validateLaunchNameField checks the required launch name rules.
func validateLaunchNameField(body launchPayload) error {
	if presentBlankString(body.fields, "name", body.Name) {
		return badRequestError{message: "name must not be blank"}
	}
	if strings.TrimSpace(body.Name) == "" && strings.TrimSpace(body.RepositoryURL) == "" {
		return badRequestError{message: "name is required"}
	}
	return nil
}

// validateLaunchRepositoryField checks optional repository URL shape.
func validateLaunchRepositoryField(body launchPayload) error {
	if presentBlankString(body.fields, "repository_url", body.RepositoryURL) {
		return badRequestError{message: "repository_url must not be blank"}
	}
	return nil
}

// validateLaunchMarqueeField checks optional Marquee references.
func validateLaunchMarqueeField(body launchPayload) error {
	if body.blankMarqueeID {
		return badRequestError{message: "marquee_id must not be blank"}
	}
	return nil
}

// validateLaunchBooleanFields validates optional boolean launch fields.
func validateLaunchBooleanFields(body launchPayload) error {
	if body.fields.Has("create_playground") && body.CreatePlayground == nil {
		return badRequestError{message: "create_playground must be true or false"}
	}
	if body.fields.Has("persist_volumes") && body.PersistVolumes == nil {
		return badRequestError{message: "persist_volumes must be true or false"}
	}
	return nil
}

// validateLaunchObjectFields validates launch maps and service overrides.
func validateLaunchObjectFields(body launchPayload) error {
	if body.invalidEnvOverrides != "" {
		return badRequestError{message: body.invalidEnvOverrides}
	}
	if err := request.ValidateStringMapField(body.fields.Has("env_overrides"), body.EnvOverrides, "env_overrides", false); err != nil {
		return badRequestError{message: err.Error()}
	}
	if body.invalidVariables != "" {
		return badRequestError{message: body.invalidVariables}
	}
	if err := request.ValidateStringMapField(body.fields.Has("variables"), body.Variables, "variables", false); err != nil {
		return badRequestError{message: err.Error()}
	}
	if err := request.ValidateNonEmptyObjectMapField(body.fields.Has("services"), body.Services, "services"); err != nil {
		return badRequestError{message: err.Error()}
	}
	return nil
}

// validateLaunchSubdomainFields validates explicit service-name subdomain overrides.
func validateLaunchSubdomainFields(body launchPayload) error {
	if body.nullServiceSubdomains {
		return badRequestError{message: "service_subdomains must be an object"}
	}
	if body.fields.Has("service_subdomains") && len(body.ServiceSubdomains) == 0 {
		return badRequestError{message: "service_subdomains must not be empty"}
	}
	if body.blankServiceSubdomains {
		return badRequestError{message: "service_subdomains keys and values must not be blank"}
	}
	if body.ambiguousSubdomains {
		return badRequestError{message: "service_subdomains keys must be unique after trimming"}
	}
	return nil
}

// validateLaunchRuntimeFields rejects Playground-only data for Playspec-only launches.
func validateLaunchRuntimeFields(body launchPayload) error {
	if body.CreatePlayground == nil || *body.CreatePlayground {
		return nil
	}
	fields := launchRuntimeOnlyFields(body)
	if len(fields) == 0 {
		return nil
	}
	return apiValidationError{
		status:  http.StatusBadRequest,
		code:    "BAD_REQUEST",
		message: "runtime-only launch fields require create_playground=true",
		details: map[string]any{"fields": fields},
	}
}

// launchRuntimeOnlyFields returns unsupported fields for create_playground=false.
func launchRuntimeOnlyFields(body launchPayload) []string {
	var fields []string
	if body.fields.Has("env_overrides") {
		fields = append(fields, "field:env_overrides")
	}
	for serviceName, raw := range body.Services {
		override, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := override["auth_password"]; ok {
			fields = append(fields, "field:services."+serviceName+".auth_password")
		}
	}
	sort.Strings(fields)
	return fields
}
