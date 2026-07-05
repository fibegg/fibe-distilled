package service

// serviceURL builds the public URL for one routed service.
func serviceURL(scheme string, rootDomain string, subdomain string) string {
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + hostRule(rootDomain, subdomain)
}

// hostRule resolves @/blank subdomains against the Marquee root domain.
func hostRule(rootDomain string, subdomain string) string {
	if subdomain == "" || subdomain == "@" {
		return rootDomain
	}
	return subdomain + "." + rootDomain
}
