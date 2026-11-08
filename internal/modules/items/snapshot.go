package items

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

type Mode string

const (
	Stackable  Mode = "stackable"
	Individual Mode = "individual"
)

type Text struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Snapshot has no maps or pointers: captures are detached, comparable values.
type Snapshot struct {
	Version   int    `json:"version"`
	CatalogID string `json:"catalog_id"`
	Mode      Mode   `json:"mode"`
	Rarity    string `json:"rarity"`
	English   Text   `json:"en"`
	Russian   Text   `json:"ru"`
	// Omit zero components to preserve pre-component version-1 bytes and digests.
	Components Components `json:"components,omitzero"`
}

func (s Snapshot) Text(language string) Text {
	if language == "ru" {
		return s.Russian
	}
	return s.English
}

func (s Snapshot) Canonical() ([]byte, error) {
	return json.Marshal(s)
}

func (s Snapshot) Equal(other Snapshot) bool {
	return s == other
}

func Digest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (s Snapshot) Validate() error {
	if s.Version != 1 {
		return errors.New("unsupported snapshot version")
	}
	return Validate(s.CatalogID, Definition{
		Name:        "name",
		Description: "description",
		Rarity:      s.Rarity,
		Mode:        s.Mode,
		Components:  s.Components,
	}, func(language, key string) (string, bool) {
		text := s.Text(language)
		if key == "name" {
			return text.Name, true
		}
		return text.Description, true
	})
}
