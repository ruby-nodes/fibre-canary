// Package buildinfo records the network/chain tuples and the application
// version this build has been validated against. Runtime compatibility checks
// are added with the network client.
package buildinfo

import "strings"

const (
	ConfigVersion = 1

	CelestiaAppModule  = "github.com/celestiaorg/celestia-app/v10"
	CelestiaAppVersion = "v10.4.0"
	CelestiaAppCommit  = "5187d2fb5eb8bc4b534c74724882943c54253ae9"
)

// Chain is one network/chain-id pair this build is allowed to run against.
type Chain struct {
	Network string
	ChainID string
}

// SupportedChains lists every chain this build accepts. Celestia tags a
// "-mocha" prerelease and the matching plain release from the identical
// commit, so one pinned CelestiaAppVersion can safely serve both Mocha and
// Mainnet once Mainnet has activated that version. See "Switching networks"
// in README.md before adding, removing, or changing an entry here.
var SupportedChains = []Chain{
	{Network: "mocha", ChainID: "mocha-5"},
	{Network: "mainnet", ChainID: "celestia"},
}

// SupportsChain reports whether network/chainID is one of SupportedChains.
func SupportsChain(network, chainID string) bool {
	for _, c := range SupportedChains {
		if c.Network == network && c.ChainID == chainID {
			return true
		}
	}
	return false
}

// SupportedChainsText renders SupportedChains for display, e.g.
// "mocha/mocha-5, mainnet/celestia".
func SupportedChainsText() string {
	parts := make([]string, len(SupportedChains))
	for i, c := range SupportedChains {
		parts[i] = c.Network + "/" + c.ChainID
	}
	return strings.Join(parts, ", ")
}
