package localization

import "fmt"

// CatalogError identifies a startup validation failure whose diagnostic is safe
// to expose to operators. Its private message contains only schema-owned keys,
// supported locale names, and fixed categories; resource values and validator
// causes are never retained.
type CatalogError struct{ message string }

func (e *CatalogError) Error() string { return e.message }

func invalidCatalog(format string, args ...any) *CatalogError {
	return &CatalogError{message: fmt.Sprintf(format, args...)}
}
