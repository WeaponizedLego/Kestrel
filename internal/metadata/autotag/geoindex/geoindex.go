// Package geoindex maps GPS coordinates to a nearest city/country
// using an embedded GeoNames dataset.
//
// The full implementation (cities500.txt + a pure-Go kd-tree) is a
// post-MVP task — see docs/assisted-tagging.md. This file provides the
// interface seam so the rest of the code can depend on it today and
// be swapped to the real lookup without an API change. Until a dataset
// is embedded, New() returns a zero-result index: autotag.Options.Geo
// can accept it without branching and no place tags are emitted.
package geoindex

// Index resolves (lat, lon) pairs to a place name plus ISO country
// code. Safe for concurrent use by many goroutines after New returns.
type Index struct {
	// cities will hold the kd-tree once the GeoNames dataset is wired
	// up. Zero-value (nil) means "no data loaded" and Lookup returns
	// ok=false for every query.
	cities kdTree
}

// New returns an Index ready for concurrent Lookup calls. Today the
// returned index is empty; when the cities500 dataset is embedded the
// loader will populate cities here at startup.
func New() *Index {
	return &Index{}
}

// Lookup returns the nearest city's name and ISO country code for the
// given coordinates. Returns ok=false when no city is within the
// configured radius or when the index is empty (default today).
//
// Signature matches autotag.GeoLookup so *Index can be passed in
// directly via Options.Geo.
func (i *Index) Lookup(lat, lon float64) (string, string, bool) {
	if i == nil || i.cities == nil {
		return "", "", false
	}
	return i.cities.nearest(lat, lon)
}

// kdTree is the interface the real dataset will satisfy. Keeping it
// unexported documents that geoindex owns the storage format and
// outsiders can only go through Index.
type kdTree interface {
	nearest(lat, lon float64) (place, countryISO string, ok bool)
}
