package packages

// SearchPackages searches every source in order and returns the hits
// deduplicated by group:artifact (the first source to surface a package wins).
// Port of searchPackages in src/packages/resolver.ts.
func SearchPackages(query string, sources []PackageSource) ([]Coordinates, error) {
	seen := make(map[PackageKey]struct{})
	var result []Coordinates
	for _, source := range sources {
		hits, err := source.Search(query)
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			key := hit.Key()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, hit)
		}
	}
	return result, nil
}
