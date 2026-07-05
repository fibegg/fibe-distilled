package service

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	// traefikMiddlewarePrefix prefixes Traefik middleware label keys.
	traefikMiddlewarePrefix = "traefik.http.middlewares."
	// traefikMiddlewareSuffix names the basic-auth user label suffix.
	traefikMiddlewareSuffix = ".basicauth.users"
)

// addInternalRouteAuth attaches basic auth middleware for internal services.
func addInternalRouteAuth(labels map[string]any, router string, scheme string, visibility string, password string, playgroundID int64) error {
	if visibility != "internal" || hasBasicAuthUsers(labels) {
		return nil
	}
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("internal route %q requires a Basic Auth password", router)
	}
	users, err := basicAuthUsers(password, playgroundID)
	if err != nil {
		return err
	}
	middleware := router + "-auth"
	labels[traefikMiddlewarePrefix+middleware+traefikMiddlewareSuffix] = users
	labels[routerMiddlewareLabel(router, scheme)] = appendMiddleware(labels[routerMiddlewareLabel(router, scheme)], middleware)
	return nil
}

// routerMiddlewareLabel returns the active router middleware label key.
func routerMiddlewareLabel(router string, scheme string) string {
	if scheme == "https" {
		return "traefik.http.routers." + router + "-secure.middlewares"
	}
	return "traefik.http.routers." + router + "-http.middlewares"
}

// hasBasicAuthUsers reports whether a service already defines basic auth.
func hasBasicAuthUsers(labels map[string]any) bool {
	for key := range labels {
		if strings.HasPrefix(key, traefikMiddlewarePrefix) && strings.HasSuffix(key, traefikMiddlewareSuffix) {
			return true
		}
	}
	return false
}

// basicAuthUsers returns a Traefik basic-auth users value.
func basicAuthUsers(password string, playgroundID int64) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("generate internal basic auth: %w", err)
	}
	escaped := strings.ReplaceAll(string(hash), "$", "$$")
	if playgroundID > 0 {
		escaped += "-" + strconv.FormatInt(playgroundID, 10)
	}
	return "playground:" + escaped, nil
}

// appendMiddleware adds one middleware name without duplicates.
func appendMiddleware(existing any, middleware string) string {
	names := middlewareNames(existing)
	for _, name := range names {
		if strings.TrimSpace(name) == middleware {
			return strings.Join(cleanUniqueStrings(names), ",")
		}
	}
	names = append(names, middleware)
	return strings.Join(cleanUniqueStrings(names), ",")
}

// middlewareNames normalizes a Traefik middleware list label.
func middlewareNames(existing any) []string {
	switch value := existing.(type) {
	case nil:
		return nil
	case string:
		return strings.Split(value, ",")
	default:
		return nil
	}
}
