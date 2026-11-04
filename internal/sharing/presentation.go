package sharing

import (
	"errors"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Attribution uses the source view's frozen locale and numeric owner identity.
// It requires no username, profile lookup, or gameplay access.
func Attribution(catalog *localization.Catalog, locale localization.Locale, owner runtimecontract.UserID) (string, error) {
	if owner <= 0 {
		return "", errors.New("sharing: invalid attribution owner")
	}

	return catalog.Text(locale, localization.SharingAttribution, localization.Args{"user_id": localization.Integer(int64(owner))})
}
