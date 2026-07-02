package auth

import "strings"

// DefaultClient represents a known OAuth client configuration
// for a specific authorization server.
type DefaultClient struct {
	Issuer       string // authorization server issuer URL prefix
	ClientID     string
	ClientSecret string // optional, for confidential clients
}

// defaultClients is the built-in registry of known OAuth App client IDs.
// These are public identifiers (not secrets) — they appear in the browser
// URL during OAuth flows and are safe to distribute.
//
// Client secrets are included for convenience — desktop apps cannot
// truly protect a secret, so embedding it provides equivalent security
// to requiring every user to register their own app and configure it.
//
// If your service is not listed here, please file an issue to request support.
var defaultClients = []DefaultClient{
	{
		Issuer:       "https://github.com",
		ClientID:     "Ov23lipCk4st2cXDZixb",
		ClientSecret: "ae1f4f4238cb27683cc22b7f40543de1c6d67b08",
	},
}

// LookupDefaultClient returns the default client configuration for a given
// authorization server issuer URL, and whether a match was found.
func LookupDefaultClient(issuerURL string) (clientID, clientSecret string, ok bool) {
	for _, dc := range defaultClients {
		if strings.HasPrefix(issuerURL, dc.Issuer) {
			return dc.ClientID, dc.ClientSecret, true
		}
	}
	return "", "", false
}

// RegisterDefaultClient adds a custom default client to the registry.
// This is primarily for alayacore developers to add known services.
func RegisterDefaultClient(issuer, clientID, clientSecret string) {
	defaultClients = append(defaultClients, DefaultClient{
		Issuer:       issuer,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	})
}
