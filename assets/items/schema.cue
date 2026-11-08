package items

#Rarity: "common" | "uncommon" | "rare" | "epic" | "legendary"

#Marker: close({
	[string]: error("item component markers must have no properties")
})

#Components: close({
	consumable?: #Marker
	material?:   #Marker
	weapon?:     #Marker

	[!~"^(consumable|material|weapon)$"]: error("unsupported item component type")
})

#Item: {
	name!:        string
	description!: string
	rarity!:      #Rarity
	mode!:        "stackable" | "individual"
	components: #Components & (*{} | _)
}

#Items: {
	[string]: #Item
}

#Items
