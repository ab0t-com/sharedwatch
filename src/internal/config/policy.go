package config

type SupplyChainPolicy struct {
	AllowedGoModuleDomains []string `json:"allowed_go_module_domains"`
}

func DefaultSupplyChainPolicy() SupplyChainPolicy {
	return SupplyChainPolicy{
		AllowedGoModuleDomains: []string{"proxy.golang.org", "sum.golang.org"},
	}
}
