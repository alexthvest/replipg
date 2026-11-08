package runtime

import "github.com/yohamta/donburi"

// newWorld is called by the runtime owner during hydration.
func newWorld() donburi.World {
	return donburi.NewWorld()
}
