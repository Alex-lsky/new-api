package price_alias

import (
	"github.com/QuantumNous/new-api/types"
)

// priceAliasMap maps an unpriced model name to a priced model name whose
// pricing entries it inherits. Resolution must only run after a direct pricing
// map lookup misses, so explicitly configured values (including 0 = free)
// always win over inheritance.
var priceAliasMap = types.NewRWMap[string, string]()

func PriceAlias2JSONString() string {
	return priceAliasMap.MarshalJSONString()
}

func UpdatePriceAliasByJSONString(jsonStr string) error {
	return types.LoadFromJsonString(priceAliasMap, jsonStr)
}

func GetPriceAliasCopy() map[string]string {
	return priceAliasMap.ReadAll()
}

// GetPriceAliasSource returns the direct alias target configured for name
// (single hop, no chain following), used for display purposes.
func GetPriceAliasSource(name string) (string, bool) {
	return priceAliasMap.Get(name)
}

// ResolveModelAlias follows the alias chain starting at name and returns the
// final target. The visited set guarantees termination on self-references and
// cycles; unusable chains report false and leave callers on their default
// (unset) behavior.
func ResolveModelAlias(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	visited := map[string]bool{name: true}
	current := name
	for {
		target, ok := priceAliasMap.Get(current)
		if !ok {
			if current == name {
				return "", false
			}
			return current, true
		}
		if visited[target] {
			return "", false
		}
		visited[target] = true
		current = target
	}
}
