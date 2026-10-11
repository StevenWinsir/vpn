module asterlink/core

go 1.24.0

require (
	gopkg.in/yaml.v3 v3.0.1
	vpn/nodepolicy v0.0.0
)

// The shared policy is the single source for both the API and this Core.
replace vpn/nodepolicy => ../../shared/nodepolicy
