package locales

en: {
	"command.ping.message": "Pong!"

	"command.replipg.usage":       "Use /replipg balance to check the bot bank balance."
	"command.replipg.unavailable": "Bank balance unavailable. Try again later."
	"command.replipg.balance":     "Bot bank balance (account {account}): {amount} ∅"

	"command.admin.usage":   "Reply to a player with /admin add or /admin remove."
	"command.admin.updated": "Game admin updated for {user}."

	"menu.settings.title":    "Settings"
	"menu.settings.language": "Language"
	"language.en":            "English"
	"language.ru":            "Russian"

	"command.character.usage": "Use /character to view your current-group profile."
	"menu.character.title":    "Character"
	"menu.character.name":     "Name"
	"menu.character.id":       "ID"
	"error.name":              "Use /rename followed by a name of 1–64 characters."

	"command.give.usage":   "Reply to a player with /give <item_id> <quantity>; quantity must be an integer from 1 to 1000."
	"command.give.failed":  "Unable to grant items. Check the recipient, active item ID, quantity, and storage."
	"command.give.success": "Granted {quantity} × {item} to {recipient}."

	"command.inventory.usage": "Use /inventory to view your current-group inventory."
	"menu.inventory.title":    "Inventory"
	"menu.inventory.empty":    "Your inventory is empty."
	"menu.inventory.page":     "Page"

	"command.items.usage": "Use /items to list all active game items."
	"command.items.given": "Added 1 × {item} to your inventory."
	"menu.items.title":    "Items"
	"menu.items.empty":    "No items are available."
	"menu.items.page":     "Page"

	"command.craft.usage":          "Use /craft to combine materials from your current-group inventory."
	"menu.craft.title":             "Crafting"
	"menu.craft.empty":             "No available materials."
	"menu.craft.selected":          "Selected"
	"menu.craft.quantity":          "Quantity"
	"menu.craft.available":         "Available"
	"menu.craft.max":               "Max"
	"menu.craft.remove":            "Remove"
	"menu.craft.materials":         "Back to materials"
	"menu.craft.review":            "Review ingredients"
	"menu.craft.confirm":           "Confirm crafting"
	"menu.craft.cancel":            "Cancel"
	"menu.craft.cancelled":         "Crafting cancelled. No materials were consumed."
	"menu.craft.loss_warning":      "Crafting creates one item and consumes all selected materials on success. Technical failures consume nothing."
	"menu.craft.pending":           "Crafting is pending. Materials are not reserved; selling or using them may prevent completion."
	"menu.craft.success":           "Item created. Selected materials were consumed."
	"menu.craft.uncraftable":       "This historical attempt created no item and consumed the selected materials."
	"menu.craft.unavailable":       "Selected materials are no longer available. No materials were consumed. Revise your selection."
	"menu.craft.disabled":          "Discovery is disabled. Saved recipes remain available. No materials were consumed."
	"menu.craft.technical-failure": "The local model could not complete crafting. No materials were consumed. Review and confirm again to retry."
	"menu.craft.refresh":           "Refresh"

	"menu.craft.composition-empty": "No ingredients selected. Press Add to choose materials."
	"menu.craft.composition":       "Ingredients:"
	"menu.craft.spent":             "Consumed:"
	"menu.craft.required":          "Required"
	"menu.craft.add":               "Add"
	"menu.craft.save":              "Save"
	"menu.craft.clear":             "Clear"
	"menu.craft.close":             "Close"
	"menu.craft.closed":            "Crafting menu closed. Accepted crafting continues; check your inventory for the result."
	"menu.craft.compose-confirm":   "Confirm"
	"menu.craft.repeat":            "Repeat"
	"menu.craft.new":               "New craft"
	"menu.craft.inventory":         "Inventory"
	"menu.craft.use-result":        "Use in crafting"
	"menu.craft.retry":             "Retry"
	"menu.craft.change":            "Change composition"

	"command.shop.usage": "Use /shop to buy, sell, and view your current-group orders."

	"menu.shop.title":              "Shop"
	"menu.shop.buy":                "Buy"
	"menu.shop.sell":               "Sell"
	"menu.shop.orders":             "My orders"
	"menu.shop.empty":              "No matching items are available."
	"menu.shop.no_orders":          "You have no orders in this group."
	"menu.shop.quantity":           "Quantity"
	"menu.shop.unit":               "Unit price"
	"menu.shop.total":              "Total"
	"menu.shop.available":          "Available"
	"menu.shop.review":             "Review order"
	"menu.shop.confirm":            "Confirm"
	"menu.shop.changed":            "Terms changed. Review and confirm the updated order."
	"menu.shop.unavailable":        "Bank unavailable."
	"menu.shop.failed":             "Unable to create this order. Check the current offer and available quantity."
	"menu.shop.refresh":            "Refresh"
	"menu.shop.max":                "Max"
	"menu.shop.owned":              "Owned"
	"menu.shop.reserved":           "Reserved"
	"menu.shop.sale_available":     "Sale available"
	"menu.shop.account_help":       "The bank could not complete this operation. Check that you have a USER account with the community bank; the operator may also need to check bank access and treasury funds."
	"menu.shop.buyer_funds":        "You did not have enough bank funds to pay this invoice. Add funds before placing a new purchase order."
	"menu.shop.buyer_refused":      "You declined this invoice. No replacement payment is created automatically."
	"menu.shop.unresolved_hold":    "Settlement is unresolved; reserved items remain held while the original operation is reconciled."
	"menu.shop.unresolved_payment": "Settlement is unresolved; the original payment operation is being reconciled. No replacement invoice is created."

	"menu.order.title":                   "Order"
	"menu.order.status":                  "Status"
	"menu.order.created":                 "Preparing invoice"
	"menu.order.requesting":              "Requesting invoice"
	"menu.order.awaiting-payment":        "Awaiting payment"
	"menu.order.paid":                    "Paid; awaiting item delivery"
	"menu.order.reserved":                "Items reserved"
	"menu.order.payout-requesting":       "Payout pending; items reserved"
	"menu.order.payout-confirmed":        "Payout confirmed; completing sale"
	"menu.order.completed":               "Completed"
	"menu.order.rejected":                "Rejected without payment"
	"menu.order.failed":                  "Failed without payment"
	"menu.order.reconciliation-required": "Outcome needs reconciliation; keep the original request"

	"menu.item.reserved":  "Reserved"
	"menu.item.available": "Available"

	"menu.item.title":       "Item"
	"menu.item.name":        "Name"
	"menu.item.description": "Description"
	"menu.item.rarity":      "Rarity"
	"menu.item.mode":        "Ownership"
	"menu.item.components":  "Components"
	"menu.item.quantity":    "Quantity"
	"menu.item.instance":    "Instance"

	"menu.back":     "Back"
	"menu.previous": "Previous"
	"menu.next":     "Next"

	"error.denied":       "You do not have permission for this action."
	"error.invalid_menu": "This menu is unavailable. Reopen the command."
	"error.persistence":  "Unable to save changes. Please try again."

	"rarity.common":    "Common"
	"rarity.uncommon":  "Uncommon"
	"rarity.rare":      "Rare"
	"rarity.epic":      "Epic"
	"rarity.legendary": "Legendary"

	"mode.stackable":  "Stackable"
	"mode.individual": "Individual"

	"item.component.none":            "None"
	"item.component.consumable.name": "Consumable"
	"item.component.material.name":   "Material"
	"item.component.weapon.name":     "Weapon"

	"item.coin.name":        "Coin"
	"item.coin.description": "A simple coin."

	"item.stick.name":        "Stick"
	"item.stick.description": "A simple wooden stick."

	"item.fumo.name":        "Fumo"
	"item.fumo.description": "A soft, seated plushie with an embroidered face. A quiet companion for your adventures."

	"item.dakimakura.name":        "Dakimakura"
	"item.dakimakura.description": "A full-length pillow featuring your favorite anime character. Takes up half the bed and all of your heart."

	"item.neco_arc.name":        "Neco-Arc"
	"item.neco_arc.description": "A tiny cat creature with an enormous talent for chaos. You did not adopt her. She just lives here now."

	"item.bread.name":        "Bread"
	"item.bread.description": "A round loaf with a crisp golden crust."

	"item.wood_plank.name":        "Wood Plank"
	"item.wood_plank.description": "A flat wooden board for frames, handles, and simple furniture."

	"item.stone.name":        "Stone"
	"item.stone.description": "A solid gray stone with a rough surface."

	"item.clay.name":        "Clay"
	"item.clay.description": "A lump of soft clay that can be shaped and fired."

	"item.sand.name":        "Sand"
	"item.sand.description": "Fine pale grains suitable for glassmaking and filling molds."

	"item.iron_ingot.name":        "Iron Ingot"
	"item.iron_ingot.description": "A plain iron bar ready to be forged into tools or fittings."

	"item.copper_wire.name":        "Copper Wire"
	"item.copper_wire.description": "A coil of flexible copper wire that conducts electricity."

	"item.leather.name":        "Leather"
	"item.leather.description": "A piece of tough, flexible leather for straps, covers, and grips."

	"item.cloth.name":        "Cloth"
	"item.cloth.description": "A square of plain woven fabric for sewing and wrapping."

	"item.thread.name":        "Thread"
	"item.thread.description": "A spool of strong sewing thread."

	"item.wool.name":        "Wool"
	"item.wool.description": "A bundle of soft wool fibers for stuffing or spinning yarn."

	"item.glass.name":        "Glass"
	"item.glass.description": "A clear piece of glass that can be cut or melted into new shapes."

	"item.rubber.name":        "Rubber"
	"item.rubber.description": "A piece of springy rubber for seals, padding, and flexible parts."

	"item.spring.name":        "Spring"
	"item.spring.description": "A small steel coil that compresses and returns to its original shape."

	"item.gear.name":        "Gear"
	"item.gear.description": "A metal wheel with evenly spaced teeth for simple mechanisms."

	"item.crystal.name":        "Crystal"
	"item.crystal.description": "A clear quartz crystal with flat faces that catch the light."

	"item.dye.name":        "Dye"
	"item.dye.description": "A small packet of blue pigment for coloring cloth and other materials."

	"item.iron_sword.name":        "Iron Sword"
	"item.iron_sword.description": "A plain iron blade with a leather-wrapped grip."

	"item.silver_ring.name":        "Silver Ring"
	"item.silver_ring.description": "A polished silver band engraved with tiny leaves."

	"item.ruby.name":        "Ruby"
	"item.ruby.description": "A deep red gemstone that catches the light."
}
