package sqlite

import (
	"github.com/alexthvest/replipg/internal/application/grants"
	shop "github.com/alexthvest/replipg/internal/application/shop"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/groups"
	"github.com/alexthvest/replipg/internal/modules/inventory"
)

var (
	_ groups.Repository     = (*Database)(nil)
	_ characters.Repository = (*Database)(nil)
	_ inventory.Reader      = (*Database)(nil)
	_ grants.UnitOfWork     = (*Database)(nil)
	_ characters.Creator    = (*grantTransaction)(nil)
	_ inventory.Writer      = (*grantTransaction)(nil)
	_ grants.Operations     = (*grantTransaction)(nil)
	_ shop.UnitOfWork       = (*Database)(nil)
	_ shop.Orders           = (*shopTransaction)(nil)
	_ shop.Requests         = (*shopTransaction)(nil)
	_ shop.Reservations     = (*shopTransaction)(nil)
)
